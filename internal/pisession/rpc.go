package pisession

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/log/v2"
)

const (
	defaultCommandTimeout = 30 * time.Second
	eventBufferSize       = 256
	rpcReaderBufferSize   = 64 << 10
)

var (
	errRPCClosing       = errors.New("pi RPC transport is closing")
	errRPCWriterStopped = errors.New("pi RPC writer stopped")
)

type rpcClient struct {
	reader         io.Reader
	writer         io.WriteCloser
	logger         *log.Logger
	commandTimeout time.Duration
	events         chan Event
	writes         chan writeRequest
	closing        chan struct{}
	pumpDone       chan struct{}
	writerDone     chan struct{}
	closeOnce      sync.Once
	nextID         atomic.Uint64
	pendingMu      sync.Mutex
	pending        map[string]chan responseResult
	writerErrMu    sync.Mutex
	writerErr      error
}

type writeRequest struct {
	value  any
	result chan error
}

type responseResult struct {
	raw json.RawMessage
	err error
}

type commandError struct {
	command string
	message string
}

func (e *commandError) Error() string {
	return fmt.Sprintf("pi RPC command %q failed: %s", e.command, e.message)
}

type responseEnvelope struct {
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func newRPCClient(reader io.Reader, writer io.WriteCloser, logger *log.Logger) *rpcClient {
	if logger == nil {
		logger = log.New(io.Discard)
	}
	client := &rpcClient{
		reader:         reader,
		writer:         writer,
		logger:         logger,
		commandTimeout: defaultCommandTimeout,
		events:         make(chan Event, eventBufferSize),
		writes:         make(chan writeRequest),
		closing:        make(chan struct{}),
		pumpDone:       make(chan struct{}),
		writerDone:     make(chan struct{}),
		pending:        make(map[string]chan responseResult),
	}
	go client.runPump()
	go client.runWriter()
	return client
}

func (c *rpcClient) command(ctx context.Context, command string, fields map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("c-%d", c.nextID.Add(1))
	payload := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		payload[key] = value
	}
	payload["id"] = id
	payload["type"] = command

	response := make(chan responseResult, 1)
	if err := c.addPending(id, response); err != nil {
		return nil, err
	}
	defer c.removePending(id, response)

	var timeout <-chan time.Time
	if command != "prompt" {
		timer := time.NewTimer(c.commandTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	timeoutErr := fmt.Errorf("%w: command %q (%s)", ErrCommandTimeout, command, id)

	request := writeRequest{value: payload, result: make(chan error, 1)}
	select {
	case c.writes <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timeout:
		return nil, timeoutErr
	case <-c.closing:
		return nil, errRPCClosing
	case <-c.writerDone:
		return nil, c.writerFailure()
	}

	select {
	case err := <-request.result:
		if err != nil {
			return nil, fmt.Errorf("write pi RPC command %q: %w", command, err)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timeout:
		return nil, timeoutErr
	case <-c.closing:
		return nil, errRPCClosing
	case <-c.writerDone:
		return nil, c.writerFailure()
	}

	var result responseResult
	select {
	case result = <-response:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timeout:
		return nil, timeoutErr
	case <-c.closing:
		return nil, errRPCClosing
	case <-c.writerDone:
		return nil, c.writerFailure()
	}
	if result.err != nil {
		return nil, result.err
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(result.raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode pi RPC response for command %q: %w", command, err)
	}
	if !envelope.Success {
		message := envelope.Error
		if message == "" {
			message = "pi returned an unsuccessful response"
		}
		return nil, &commandError{command: command, message: message}
	}
	return envelope.Data, nil
}

func (c *rpcClient) write(value any) error {
	request := writeRequest{value: value, result: make(chan error, 1)}
	select {
	case c.writes <- request:
	case <-c.closing:
		return errRPCClosing
	case <-c.writerDone:
		return c.writerFailure()
	}

	select {
	case err := <-request.result:
		if err != nil {
			return fmt.Errorf("write pi RPC record: %w", err)
		}
		return nil
	case <-c.closing:
		return errRPCClosing
	case <-c.writerDone:
		return c.writerFailure()
	}
}

func (c *rpcClient) addPending(id string, response chan responseResult) error {
	select {
	case <-c.closing:
		return errRPCClosing
	default:
	}

	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	select {
	case <-c.closing:
		return errRPCClosing
	default:
		c.pending[id] = response
		return nil
	}
}

func (c *rpcClient) removePending(id string, response chan responseResult) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.pending[id] == response {
		delete(c.pending, id)
	}
}

func (c *rpcClient) runWriter() {
	defer close(c.writerDone)
	for {
		select {
		case <-c.closing:
			c.setWriterFailure(errRPCClosing)
			return
		default:
		}

		select {
		case <-c.closing:
			c.setWriterFailure(errRPCClosing)
			return
		case request := <-c.writes:
			select {
			case <-c.closing:
				request.result <- errRPCClosing
				c.setWriterFailure(errRPCClosing)
				return
			default:
			}

			encoded, err := json.Marshal(request.value)
			if err != nil {
				request.result <- fmt.Errorf("encode pi RPC record: %w", err)
				continue
			}
			encoded = append(encoded, '\n')
			err = writeAll(c.writer, encoded)
			request.result <- err
			if err != nil {
				c.setWriterFailure(fmt.Errorf("write pi RPC record: %w", err))
				return
			}
		}
	}
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if written > 0 {
			value = value[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (c *rpcClient) setWriterFailure(err error) {
	c.writerErrMu.Lock()
	defer c.writerErrMu.Unlock()
	if c.writerErr == nil {
		c.writerErr = err
	}
}

func (c *rpcClient) writerFailure() error {
	c.writerErrMu.Lock()
	defer c.writerErrMu.Unlock()
	if c.writerErr == nil {
		return errRPCWriterStopped
	}
	return c.writerErr
}

func (c *rpcClient) runPump() {
	defer close(c.pumpDone)
	reader := bufio.NewReaderSize(c.reader, rpcReaderBufferSize)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(line) > 0 && !c.demux(line) {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.logger.Error("pi RPC reader stopped", "error", err)
			}
			return
		}
	}
}

func (c *rpcClient) demux(raw []byte) bool {
	var head *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head == nil {
		c.logger.Error("invalid pi RPC record", "error", err)
		return true
	}

	if head.Type == "response" {
		c.resolvePending(head.ID, raw)
		return true
	}

	select {
	case c.events <- Event{Type: head.Type, Raw: json.RawMessage(raw)}:
		return true
	case <-c.closing:
		return false
	}
}

func (c *rpcClient) resolvePending(id string, raw json.RawMessage) {
	c.pendingMu.Lock()
	response, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()

	if !ok {
		c.logger.Error("unmatched pi RPC response", "id", id)
		return
	}
	response <- responseResult{raw: raw}
}

func (c *rpcClient) failPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan responseResult)
	c.pendingMu.Unlock()

	for _, response := range pending {
		response <- responseResult{err: err}
	}
}

func (c *rpcClient) close() {
	c.closeOnce.Do(func() {
		close(c.closing)
	})
}
