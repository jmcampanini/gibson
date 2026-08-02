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
	reader          io.Reader
	writer          io.WriteCloser
	logger          *log.Logger
	commandTimeout  time.Duration
	events          chan Event
	writes          chan writeRequest
	closing         chan struct{}
	pumpDone        chan struct{}
	writerDone      chan struct{}
	closeOnce       sync.Once
	writerCloseOnce sync.Once
	nextID          atomic.Uint64
	pendingMu       sync.Mutex
	pending         map[string]chan responseResult
	writerErrMu     sync.Mutex
	writerErr       error
}

type writeRequest struct {
	value  any
	result chan error
	done   chan struct{}
}

type responseWaitPolicy uint8

const (
	boundedResponseWait responseWaitPolicy = iota
	unboundedResponseWait
)

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
	return c.commandWithPolicy(ctx, command, fields, boundedResponseWait)
}

func (c *rpcClient) commandWithPolicy(ctx context.Context, command string, fields map[string]any, policy responseWaitPolicy) (json.RawMessage, error) {
	return c.commandWithWritePolicy(ctx, command, fields, policy, nil)
}

func (c *rpcClient) commandWithWritePolicy(ctx context.Context, command string, fields map[string]any, policy responseWaitPolicy, written chan<- struct{}) (json.RawMessage, error) {
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

	timeoutErr := fmt.Errorf("%w: command %q (%s)", ErrCommandTimeout, command, id)
	deadline := time.Now().Add(c.commandTimeout)
	request := writeRequest{value: payload, result: make(chan error, 1), done: make(chan struct{})}
	writeTimer := c.startWriteTimer(request.done, timeoutErr)

	select {
	case c.writes <- request:
	case <-ctx.Done():
		writeTimer.Stop()
		return nil, ctx.Err()
	case <-c.closing:
		writeTimer.Stop()
		return nil, c.transportFailure()
	case <-c.writerDone:
		writeTimer.Stop()
		return nil, c.transportFailure()
	}

	select {
	case err := <-request.result:
		writeTimer.Stop()
		if err != nil {
			return nil, fmt.Errorf("write pi RPC command %q: %w", command, err)
		}
		if written != nil {
			close(written)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closing:
		return nil, c.transportFailure()
	case <-c.writerDone:
		return nil, c.transportFailure()
	}

	var timeout <-chan time.Time
	var responseTimer *time.Timer
	if policy == boundedResponseWait {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, timeoutErr
		}
		responseTimer = time.NewTimer(remaining)
		defer responseTimer.Stop()
		timeout = responseTimer.C
	}

	var result responseResult
	select {
	case result = <-response:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timeout:
		return nil, timeoutErr
	case <-c.closing:
		return nil, c.transportFailure()
	case <-c.writerDone:
		return nil, c.transportFailure()
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
	timeoutErr := fmt.Errorf("%w: RPC record write", ErrCommandTimeout)
	request := writeRequest{value: value, result: make(chan error, 1), done: make(chan struct{})}
	writeTimer := c.startWriteTimer(request.done, timeoutErr)

	select {
	case c.writes <- request:
	case <-c.closing:
		writeTimer.Stop()
		return c.transportFailure()
	case <-c.writerDone:
		writeTimer.Stop()
		return c.transportFailure()
	}

	select {
	case err := <-request.result:
		writeTimer.Stop()
		if err != nil {
			return fmt.Errorf("write pi RPC record: %w", err)
		}
		return nil
	case <-c.closing:
		return c.transportFailure()
	case <-c.writerDone:
		return c.transportFailure()
	}
}

func (c *rpcClient) startWriteTimer(done <-chan struct{}, timeoutErr error) *time.Timer {
	return time.AfterFunc(c.commandTimeout, func() {
		select {
		case <-done:
			return
		default:
			c.fail(timeoutErr)
		}
	})
}

func (c *rpcClient) addPending(id string, response chan responseResult) error {
	select {
	case <-c.closing:
		return c.transportFailure()
	default:
	}

	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	select {
	case <-c.closing:
		return c.transportFailure()
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
			return
		case request := <-c.writes:
			select {
			case <-c.closing:
				close(request.done)
				request.result <- c.transportFailure()
				return
			default:
			}

			encoded, err := json.Marshal(request.value)
			if err != nil {
				close(request.done)
				request.result <- fmt.Errorf("encode pi RPC record: %w", err)
				continue
			}
			encoded = append(encoded, '\n')
			err = writeAll(c.writer, encoded)
			close(request.done)
			if err != nil {
				c.fail(fmt.Errorf("write pi RPC record: %w", err))
				request.result <- c.transportFailure()
				return
			}
			request.result <- nil
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

func (c *rpcClient) transportFailure() error {
	return c.writerFailure()
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
	c.stop(errRPCClosing)
}

func (c *rpcClient) stop(err error) {
	c.closeOnce.Do(func() {
		c.setWriterFailure(err)
		close(c.closing)
		c.failPending(err)
	})
}

func (c *rpcClient) fail(err error) {
	c.stop(err)
	c.closeWriter()
}

func (c *rpcClient) closeWriter() {
	c.writerCloseOnce.Do(func() {
		_ = c.writer.Close()
	})
}
