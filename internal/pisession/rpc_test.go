package pisession

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRPCFramingPreservesRecords(t *testing.T) {
	unicodeRecord := []byte("{\"type\":\"unicode\",\"value\":\"left\xe2\x80\xa8middle\xe2\x80\xa9right\"}")
	largeRecord := []byte(`{"type":"large","value":"` + strings.Repeat("x", (1<<20)+1) + `"}`)

	tests := []struct {
		name   string
		reader io.Reader
		want   []byte
	}{
		{
			name: "chunked",
			reader: io.MultiReader(
				strings.NewReader(`{"type":`),
				strings.NewReader(`"chunked","value":"kept"}`),
				strings.NewReader("\n"),
			),
			want: []byte(`{"type":"chunked","value":"kept"}`),
		},
		{
			name:   "CRLF",
			reader: strings.NewReader("{\"type\":\"crlf\",\"value\":\"kept\"}\r\n"),
			want:   []byte(`{"type":"crlf","value":"kept"}`),
		},
		{
			name:   "Unicode separators",
			reader: bytes.NewReader(append(append([]byte(nil), unicodeRecord...), '\n')),
			want:   unicodeRecord,
		},
		{
			name:   "record larger than one megabyte",
			reader: bytes.NewReader(append(append([]byte(nil), largeRecord...), '\n')),
			want:   largeRecord,
		},
		{
			name:   "unterminated EOF record",
			reader: strings.NewReader(`{"type":"eof","value":"kept"}`),
			want:   []byte(`{"type":"eof","value":"kept"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newRPCClient(test.reader, nopWriteCloser{Writer: io.Discard}, nil)
			waitFor(t, client.pumpDone, "RPC pump")

			require.Len(t, client.events, 1)
			event := <-client.events
			assert.JSONEq(t, string(test.want), string(event.Raw))
			assert.Equal(t, string(test.want), string(event.Raw))

			stopRPC(t, client)
		})
	}
}

func TestRPCDropsMalformedRecordsAndContinues(t *testing.T) {
	var logs bytes.Buffer
	client := newRPCClient(
		strings.NewReader("\nnot-json\nnull\n{\"type\":\"valid\",\"value\":1}\n"),
		nopWriteCloser{Writer: io.Discard},
		log.New(&logs),
	)
	waitFor(t, client.pumpDone, "RPC pump")

	require.Len(t, client.events, 1)
	event := <-client.events
	assert.Equal(t, "valid", event.Type)
	assert.JSONEq(t, `{"type":"valid","value":1}`, string(event.Raw))
	assert.NotEmpty(t, logs.String())

	stopRPC(t, client)
}

func TestRPCEventBackpressure(t *testing.T) {
	t.Run("blocks instead of dropping", func(t *testing.T) {
		input, source := io.Pipe()
		client := newRPCClient(input, nopWriteCloser{Writer: io.Discard}, nil)
		sourceDone := writeEvents(source, eventBufferSize+1)

		require.Eventually(t, func() bool {
			return len(client.events) == eventBufferSize
		}, time.Second, time.Millisecond)
		select {
		case <-client.pumpDone:
			t.Fatal("RPC pump stopped instead of applying backpressure")
		case <-time.After(20 * time.Millisecond):
		}

		first := <-client.events
		assert.Equal(t, "event", first.Type)
		waitFor(t, client.pumpDone, "RPC pump")
		require.NoError(t, <-sourceDone)

		count := 1
		for len(client.events) > 0 {
			<-client.events
			count++
		}
		assert.Equal(t, eventBufferSize+1, count)
		stopRPC(t, client)
	})

	t.Run("closing unblocks a full channel", func(t *testing.T) {
		input, source := io.Pipe()
		client := newRPCClient(input, nopWriteCloser{Writer: io.Discard}, nil)
		sourceDone := writeEvents(source, eventBufferSize+1)

		require.Eventually(t, func() bool {
			return len(client.events) == eventBufferSize
		}, time.Second, time.Millisecond)
		client.close()
		waitFor(t, client.pumpDone, "RPC pump")
		require.NoError(t, <-sourceDone)
		waitFor(t, client.writerDone, "RPC writer")
	})
}

func TestRPCConcurrentCommandsSerializeAndResolveOutOfOrder(t *testing.T) {
	piOutput, writePiOutput := io.Pipe()
	readCommands, piInput := io.Pipe()
	client := newRPCClient(piOutput, piInput, nil)
	client.commandTimeout = 2 * time.Second

	const commandCount = 24
	type commandResult struct {
		data json.RawMessage
		err  error
	}
	results := make([]chan commandResult, commandCount)
	for index := range commandCount {
		results[index] = make(chan commandResult, 1)
		go func() {
			name := fmt.Sprintf("operation-%02d", index)
			data, err := client.command(context.Background(), name, map[string]any{"sequence": index})
			results[index] <- commandResult{data: data, err: err}
		}()
	}

	peerResult := make(chan error, 1)
	go func() {
		defer closePeer(readCommands, writePiOutput)
		reader := bufio.NewReader(readCommands)
		type receivedCommand struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Sequence int    `json:"sequence"`
		}
		commands := make([]receivedCommand, 0, commandCount)
		seenIDs := make(map[string]struct{}, commandCount)
		commandID := regexp.MustCompile(`^c-[1-9][0-9]*$`)
		for range commandCount {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				peerResult <- fmt.Errorf("read command: %w", err)
				return
			}
			if len(line) == 0 || line[len(line)-1] != '\n' || bytes.Count(line, []byte{'\n'}) != 1 {
				peerResult <- fmt.Errorf("command was not one LF-terminated record: %q", line)
				return
			}
			var command receivedCommand
			if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &command); err != nil {
				peerResult <- fmt.Errorf("decode command: %w", err)
				return
			}
			if !commandID.MatchString(command.ID) {
				peerResult <- fmt.Errorf("invalid command id %q", command.ID)
				return
			}
			if _, exists := seenIDs[command.ID]; exists {
				peerResult <- fmt.Errorf("duplicate command id %q", command.ID)
				return
			}
			seenIDs[command.ID] = struct{}{}
			commands = append(commands, command)
		}

		event := []byte(`{"type":"message_update","value":"untouched"}`)
		if err := writeJSONLine(writePiOutput, json.RawMessage(event)); err != nil {
			peerResult <- err
			return
		}
		for index := len(commands) - 1; index >= 0; index-- {
			command := commands[index]
			response := fmt.Sprintf(
				`{"id":%q,"type":"response","command":%q,"success":true,"data":{ "sequence" : %d }}`+"\n",
				command.ID,
				command.Type,
				command.Sequence,
			)
			if _, err := io.WriteString(writePiOutput, response); err != nil {
				peerResult <- err
				return
			}
		}
		peerResult <- nil
	}()

	for index, resultChannel := range results {
		result := <-resultChannel
		require.NoError(t, result.err)
		assert.Equal(t, fmt.Sprintf(`{ "sequence" : %d }`, index), string(result.data))
	}
	require.NoError(t, <-peerResult)
	waitFor(t, client.pumpDone, "RPC pump")

	require.Len(t, client.events, 1)
	event := <-client.events
	assert.Equal(t, "message_update", event.Type)
	assert.Equal(t, `{"type":"message_update","value":"untouched"}`, string(event.Raw))

	client.close()
	waitFor(t, client.writerDone, "RPC writer")
}

func TestRPCCommandFailuresAndTimeouts(t *testing.T) {
	t.Run("pi-declared failure", func(t *testing.T) {
		piOutput, writePiOutput := io.Pipe()
		readCommands, piInput := io.Pipe()
		client := newRPCClient(piOutput, piInput, nil)
		client.commandTimeout = time.Second

		peerResult := make(chan error, 1)
		go func() {
			defer closePeer(readCommands, writePiOutput)
			reader := bufio.NewReader(readCommands)
			line, err := reader.ReadBytes('\n')
			if err != nil {
				peerResult <- err
				return
			}
			var command struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(line, &command); err != nil {
				peerResult <- err
				return
			}
			peerResult <- writeJSONLine(writePiOutput, map[string]any{
				"id": command.ID, "type": "response", "command": command.Type,
				"success": false, "error": "model unavailable",
			})
		}()

		_, err := client.command(context.Background(), "prompt", map[string]any{"message": "hello"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prompt")
		assert.Contains(t, err.Error(), "model unavailable")
		require.NoError(t, <-peerResult)
		waitFor(t, client.pumpDone, "RPC pump")
		assert.Empty(t, client.events)

		client.close()
		waitFor(t, client.writerDone, "RPC writer")
	})

	t.Run("transport failure resolves pending command", func(t *testing.T) {
		input, source := io.Pipe()
		output := &synchronizedBuffer{}
		client := newRPCClient(input, output, nil)
		client.commandTimeout = time.Second
		transportErr := errors.New("transport stopped")
		result := make(chan error, 1)

		go func() {
			_, err := client.command(context.Background(), "get_state", nil)
			result <- err
		}()
		require.Eventually(t, func() bool {
			return bytes.HasSuffix(output.Bytes(), []byte{'\n'})
		}, time.Second, time.Millisecond)
		client.failPending(transportErr)
		require.ErrorIs(t, <-result, transportErr)

		client.close()
		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		waitFor(t, client.writerDone, "RPC writer")
	})

	t.Run("output EOF fails a pending prompt", func(t *testing.T) {
		piOutput, writePiOutput := io.Pipe()
		readCommands, piInput := io.Pipe()
		client := newRPCClient(piOutput, piInput, nil)
		client.commandTimeout = time.Second
		result := make(chan error, 1)

		go func() {
			_, err := client.commandWithPolicy(context.Background(), "prompt", map[string]any{"message": "waiting"}, unboundedResponseWait)
			result <- err
		}()
		_, err := bufio.NewReader(readCommands).ReadBytes('\n')
		require.NoError(t, err)
		require.NoError(t, writePiOutput.Close())

		promptErr := <-result
		require.ErrorIs(t, promptErr, ErrTransportClosed)
		require.ErrorIs(t, promptErr, io.EOF)
		waitFor(t, client.pumpDone, "RPC pump")
		waitFor(t, client.writerDone, "RPC writer")
		_, err = readCommands.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("default command timeout and late response", func(t *testing.T) {
		input, source := io.Pipe()
		output := &synchronizedBuffer{}
		var logs bytes.Buffer
		client := newRPCClient(input, output, log.New(&logs))
		client.commandTimeout = 20 * time.Millisecond

		_, err := client.command(context.Background(), "get_state", nil)
		require.ErrorIs(t, err, ErrCommandTimeout)

		var command struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &command))
		require.NotEmpty(t, command.ID)
		require.NoError(t, writeJSONLine(source, map[string]any{
			"id": command.ID, "type": "response", "command": "get_state", "success": true,
		}))
		eventRaw := []byte(`{"type":"agent_settled","value":"kept"}`)
		require.NoError(t, writeJSONLine(source, json.RawMessage(eventRaw)))

		event := receiveEvent(t, client.events)
		assert.Equal(t, string(eventRaw), string(event.Raw))
		assert.NotEmpty(t, logs.String())
		assert.Empty(t, client.events)

		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		stopRPC(t, client)
	})

	t.Run("prompt accepts a delayed response", func(t *testing.T) {
		input, source := io.Pipe()
		output := &synchronizedBuffer{}
		client := newRPCClient(input, output, nil)
		client.commandTimeout = 20 * time.Millisecond
		result := make(chan error, 1)

		go func() {
			_, err := client.commandWithPolicy(context.Background(), "prompt", map[string]any{"message": "handled"}, unboundedResponseWait)
			result <- err
		}()
		require.Eventually(t, func() bool {
			return bytes.HasSuffix(output.Bytes(), []byte{'\n'})
		}, time.Second, time.Millisecond)
		select {
		case err := <-result:
			t.Fatalf("prompt returned before its delayed response: %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		var command struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &command))
		require.NoError(t, writeJSONLine(source, map[string]any{
			"id": command.ID, "type": "response", "command": "prompt", "success": true,
		}))
		require.NoError(t, <-result)

		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		stopRPC(t, client)
	})

	t.Run("pending prompt waits for caller cancellation", func(t *testing.T) {
		input, source := io.Pipe()
		output := &synchronizedBuffer{}
		client := newRPCClient(input, output, nil)
		client.commandTimeout = 20 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)

		go func() {
			_, err := client.commandWithPolicy(ctx, "prompt", map[string]any{"message": "dialog"}, unboundedResponseWait)
			result <- err
		}()
		require.Eventually(t, func() bool {
			return bytes.HasSuffix(output.Bytes(), []byte{'\n'})
		}, time.Second, time.Millisecond)
		dialogRaw := []byte(`{"type":"extension_ui_request","method":"confirm","message":"Continue?"}`)
		require.NoError(t, writeJSONLine(source, json.RawMessage(dialogRaw)))
		assert.Equal(t, string(dialogRaw), string(receiveEvent(t, client.events).Raw))
		select {
		case err := <-result:
			t.Fatalf("pending prompt returned without caller cancellation: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)

		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		stopRPC(t, client)
	})

	t.Run("caller deadline wins", func(t *testing.T) {
		input, source := io.Pipe()
		client := newRPCClient(input, &synchronizedBuffer{}, nil)
		client.commandTimeout = time.Second
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := client.command(ctx, "get_state", nil)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotErrorIs(t, err, ErrCommandTimeout)

		client.close()
		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		waitFor(t, client.writerDone, "RPC writer")
	})
}

func TestRPCPromptWriteTimeoutIsFatal(t *testing.T) {
	input, source := io.Pipe()
	writer := newBlockingWriteCloser()
	client := newRPCClient(input, writer, nil)
	client.commandTimeout = 40 * time.Millisecond

	promptResult := make(chan error, 1)
	go func() {
		_, err := client.commandWithPolicy(context.Background(), "prompt", map[string]any{"message": "blocked"}, unboundedResponseWait)
		promptResult <- err
	}()
	waitFor(t, writer.entered, "blocked prompt write")

	otherResult := make(chan error, 1)
	go func() {
		_, err := client.command(context.Background(), "get_state", nil)
		otherResult <- err
	}()

	promptErr := <-promptResult
	otherErr := <-otherResult
	require.ErrorIs(t, promptErr, ErrCommandTimeout)
	require.ErrorIs(t, otherErr, ErrCommandTimeout)
	assert.Equal(t, promptErr.Error(), otherErr.Error())
	waitFor(t, client.writerDone, "RPC writer")
	select {
	case <-client.closing:
	default:
		t.Fatal("write timeout did not close the transport")
	}
	require.NoError(t, source.Close())
	waitFor(t, client.pumpDone, "RPC pump")
}

func TestRPCDemultiplexedResponseWinsTerminalClosureRace(t *testing.T) {
	input, source := io.Pipe()
	writer := newGatedWriteCloser(nil)
	client := newRPCClient(input, writer, nil)
	client.commandTimeout = time.Minute
	t.Cleanup(func() {
		writer.release()
		_ = source.Close()
		client.close()
	})

	var data json.RawMessage
	var commandErr error
	commandDone := make(chan struct{})
	go func() {
		data, commandErr = client.command(context.Background(), "get_state", nil)
		close(commandDone)
	}()
	waitFor(t, writer.entered, "gated command write")

	var command struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &command))
	require.NotEmpty(t, command.ID)
	response := []byte(fmt.Sprintf(
		`{"id":%q,"type":"response","command":"get_state","success":true,"data":{"state":"ready"}}`,
		command.ID,
	))
	require.True(t, client.demux(response))

	require.NoError(t, source.Close())
	waitFor(t, client.pumpDone, "RPC pump")
	waitFor(t, writer.closed, "RPC writer close")
	waitFor(t, commandDone, "RPC command")
	require.NoError(t, commandErr)
	assert.JSONEq(t, `{"state":"ready"}`, string(data))

	writer.release()
	waitFor(t, client.writerDone, "RPC writer")
}

func TestRPCFatalWriteFailureResolvesAllPendingCommands(t *testing.T) {
	input, source := io.Pipe()
	writeErr := errors.New("fatal write failure")
	writer := newGatedWriteCloser(writeErr)
	client := newRPCClient(input, writer, nil)
	client.commandTimeout = time.Minute
	t.Cleanup(func() {
		writer.release()
		_ = source.Close()
		client.close()
	})

	const commandCount = 4
	results := make([]error, commandCount)
	done := make([]chan struct{}, commandCount)
	startCommand := func(index int) {
		done[index] = make(chan struct{})
		go func() {
			_, results[index] = client.command(context.Background(), fmt.Sprintf("operation-%d", index), nil)
			close(done[index])
		}()
	}

	startCommand(0)
	waitFor(t, writer.entered, "gated command write")
	for index := 1; index < commandCount; index++ {
		startCommand(index)
	}
	require.Eventually(t, func() bool {
		client.pendingMu.Lock()
		defer client.pendingMu.Unlock()
		return len(client.pending) == commandCount
	}, 5*time.Second, time.Millisecond)

	writer.release()
	for index := range commandCount {
		waitFor(t, done[index], fmt.Sprintf("RPC command %d", index))
		require.ErrorIs(t, results[index], writeErr)
	}
	for index := 1; index < commandCount; index++ {
		assert.Equal(t, results[0].Error(), results[index].Error())
	}
	waitFor(t, writer.closed, "RPC writer close")
	waitFor(t, client.writerDone, "RPC writer")

	require.NoError(t, source.Close())
	waitFor(t, client.pumpDone, "RPC pump")
}

func TestRPCWriterCompletesShortWritesAndReportsFailures(t *testing.T) {
	t.Run("short writes", func(t *testing.T) {
		input, source := io.Pipe()
		output := &shortWriteBuffer{limit: 1}
		client := newRPCClient(input, output, nil)
		client.commandTimeout = time.Second

		result := make(chan error, 1)
		go func() {
			_, err := client.command(context.Background(), "get_state", nil)
			result <- err
		}()
		require.Eventually(t, func() bool {
			return bytes.HasSuffix(output.Bytes(), []byte{'\n'})
		}, time.Second, time.Millisecond)
		require.NoError(t, writeJSONLine(source, map[string]any{
			"id": "c-1", "type": "response", "command": "get_state", "success": true,
		}))
		require.NoError(t, <-result)

		client.close()
		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		waitFor(t, client.writerDone, "RPC writer")
	})

	t.Run("write failure", func(t *testing.T) {
		input, source := io.Pipe()
		writeErr := errors.New("broken pipe")
		client := newRPCClient(input, failingWriteCloser{err: writeErr}, nil)
		client.commandTimeout = time.Second

		_, err := client.command(context.Background(), "get_state", nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, writeErr)

		waitFor(t, client.writerDone, "RPC writer")
		require.NoError(t, source.Close())
		waitFor(t, client.pumpDone, "RPC pump")
		client.close()
	})
}

func writeEvents(writer *io.PipeWriter, count int) <-chan error {
	result := make(chan error, 1)
	go func() {
		defer func() { _ = writer.Close() }()
		for index := range count {
			if err := writeJSONLine(writer, map[string]any{"type": "event", "index": index}); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}()
	return result
}

func writeJSONLine(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = writer.Write(encoded)
	return err
}

func closePeer(reader *io.PipeReader, writer *io.PipeWriter) {
	_ = reader.Close()
	_ = writer.Close()
}

func stopRPC(t testing.TB, client *rpcClient) {
	t.Helper()
	client.close()
	waitFor(t, client.writerDone, "RPC writer")
}

func waitFor(t testing.TB, done <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not stop", name)
	}
}

func receiveEvent(t testing.TB, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("event was not received")
		return Event{}
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error {
	return nil
}

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(value)
}

func (b *synchronizedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.Buffer.Bytes()...)
}

func (b *synchronizedBuffer) Close() error {
	return nil
}

type shortWriteBuffer struct {
	mu    sync.Mutex
	value bytes.Buffer
	limit int
}

func (b *shortWriteBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(value) > b.limit {
		value = value[:b.limit]
	}
	return b.value.Write(value)
}

func (b *shortWriteBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.value.Bytes()...)
}

func (b *shortWriteBuffer) Close() error {
	return nil
}

type blockingWriteCloser struct {
	entered   chan struct{}
	closed    chan struct{}
	enterOnce sync.Once
	closeOnce sync.Once
}

func newBlockingWriteCloser() *blockingWriteCloser {
	return &blockingWriteCloser{entered: make(chan struct{}), closed: make(chan struct{})}
}

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	w.enterOnce.Do(func() { close(w.entered) })
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockingWriteCloser) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return nil
}

type gatedWriteCloser struct {
	mu          sync.Mutex
	value       bytes.Buffer
	err         error
	entered     chan struct{}
	released    chan struct{}
	closed      chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	closeOnce   sync.Once
}

func newGatedWriteCloser(err error) *gatedWriteCloser {
	return &gatedWriteCloser{
		err:      err,
		entered:  make(chan struct{}),
		released: make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (w *gatedWriteCloser) Write(value []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.value.Write(value)
	w.mu.Unlock()
	w.enterOnce.Do(func() { close(w.entered) })
	<-w.released
	if w.err != nil {
		return 0, w.err
	}
	return len(value), nil
}

func (w *gatedWriteCloser) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.value.Bytes()...)
}

func (w *gatedWriteCloser) release() {
	w.releaseOnce.Do(func() { close(w.released) })
}

func (w *gatedWriteCloser) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return nil
}

type failingWriteCloser struct {
	err error
}

func (w failingWriteCloser) Write([]byte) (int, error) {
	return 0, w.err
}

func (failingWriteCloser) Close() error {
	return nil
}
