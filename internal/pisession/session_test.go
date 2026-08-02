package pisession

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"charm.land/log/v2"

	"github.com/jmcampanini/gibson/internal/pitest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIResolutionMarshal(t *testing.T) {
	confirmed := false
	encoded, err := json.Marshal(UIResolution{Confirmed: &confirmed})
	require.NoError(t, err)
	assert.JSONEq(t, `{"confirmed":false}`, string(encoded))
}

func TestSessionBasicLifecycle(t *testing.T) {
	cfg := newSessionTestConfig(t, "s-20260728-basic1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)

	assert.Equal(t, cfg.SessionID, session.ID())
	assert.Positive(t, session.PID())
	processGroup, err := syscall.Getpgid(session.PID())
	require.NoError(t, err)
	assert.Equal(t, session.PID(), processGroup)
	assert.NotEqual(t, syscall.Getpgrp(), processGroup)
	select {
	case <-session.Done():
		t.Fatal("session exited before use")
	default:
	}

	stateRaw, err := session.GetState(ctx)
	require.NoError(t, err)
	var state struct {
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
		IsStreaming bool   `json:"isStreaming"`
	}
	require.NoError(t, json.Unmarshal(stateRaw, &state))
	assert.Equal(t, cfg.SessionID, state.SessionID)
	assert.Equal(t, cfg.SessionDir, filepath.Dir(state.SessionFile))
	assert.False(t, state.IsStreaming)

	entries, leafID, err := session.GetEntries(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Empty(t, leafID)

	require.NoError(t, session.Prompt(ctx, "Remember this prompt", ""))
	var events []Event
	for {
		event := receiveSessionEvent(t, session.Events())
		events = append(events, event)
		var head struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(event.Raw, &head))
		assert.Equal(t, event.Type, head.Type)
		assert.Equal(t, bytes.TrimSpace(event.Raw), []byte(event.Raw))
		if event.Type == "agent_settled" {
			break
		}
	}

	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
	}
	assert.Equal(t, []string{
		"agent_start",
		"message_start",
		"message_update",
		"message_update",
		"message_update",
		"message_end",
		"agent_end",
		"agent_settled",
	}, eventTypes)

	entries, leafID, err = session.GetEntries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var finalEntry struct {
		ID      string `json:"id"`
		Message struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
		} `json:"message"`
	}
	require.NoError(t, json.Unmarshal(entries[1], &finalEntry))
	assert.Equal(t, finalEntry.ID, leafID)
	assert.Equal(t, "assistant", finalEntry.Message.Role)
	assert.Equal(t, "stop", finalEntry.Message.StopReason)
	assertSessionFileMatchesEntries(t, state.SessionFile, cfg, entries)

	require.NoError(t, session.Close(ctx))
	select {
	case <-session.Done():
	default:
		t.Fatal("Close returned before session completion")
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, session.ExitErr(), &exitErr)
	assert.Equal(t, 143, exitErr.ExitCode())
	_, open := <-session.Events()
	assert.False(t, open)
	_, err = session.stderr.Stat()
	assert.Error(t, err)
}

func TestSessionStartPromptReturnsAfterWriteBeforeAcceptance(t *testing.T) {
	piOutput, writePiOutput := io.Pipe()
	readCommands, piInput := io.Pipe()
	client := newRPCClient(piOutput, piInput, nil)
	client.commandTimeout = time.Second
	session := &Session{rpc: client}
	releaseResponse := make(chan struct{})
	peerResult := make(chan error, 1)
	go func() {
		defer closePeer(readCommands, writePiOutput)
		line, err := bufio.NewReader(readCommands).ReadBytes('\n')
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
		if command.Type != "prompt" {
			peerResult <- fmt.Errorf("expected prompt, got %q", command.Type)
			return
		}
		<-releaseResponse
		peerResult <- writeJSONLine(writePiOutput, map[string]any{
			"id": command.ID, "type": "response", "command": "prompt", "success": true,
		})
	}()

	acceptance, err := session.StartPrompt(context.Background(), "hello", "")
	require.NoError(t, err)
	select {
	case err := <-acceptance:
		t.Fatalf("prompt acceptance resolved before peer response: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseResponse)
	require.NoError(t, <-acceptance)
	require.NoError(t, <-peerResult)
	waitFor(t, client.pumpDone, "RPC pump")
	stopRPC(t, client)
}

func TestSessionTypedCommands(t *testing.T) {
	cfg := newSessionTestConfig(t, "s-20260728-typed1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)

	state, err := session.GetState(ctx)
	require.NoError(t, err)
	var stateData map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(state, &stateData))
	assert.JSONEq(t, `"`+cfg.SessionID+`"`, string(stateData["sessionId"]))
	assert.JSONEq(t, `false`, string(stateData["isStreaming"]))
	assert.NotEmpty(t, stateData["model"])
	assert.NotContains(t, stateData, "type")
	assert.NotContains(t, stateData, "success")

	stats, err := session.GetSessionStats(ctx)
	require.NoError(t, err)
	var statsData struct {
		SessionID     string `json:"sessionId"`
		TotalMessages int    `json:"totalMessages"`
		ContextUsage  struct {
			ContextWindow int `json:"contextWindow"`
		} `json:"contextUsage"`
	}
	require.NoError(t, json.Unmarshal(stats, &statsData))
	assert.Equal(t, cfg.SessionID, statsData.SessionID)
	assert.Zero(t, statsData.TotalMessages)
	assert.Equal(t, 128000, statsData.ContextUsage.ContextWindow)

	require.NoError(t, session.SetSessionName(ctx, "Named session"))
	nameEvent := receiveSessionEvent(t, session.Events())
	assert.Equal(t, "session_info_changed", nameEvent.Type)
	assert.JSONEq(t, `{"type":"session_info_changed","name":"Named session"}`, string(nameEvent.Raw))

	entries, leafID, err := session.GetEntries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.NotEmpty(t, leafID)

	tail, currentLeafID, err := session.GetEntries(ctx, leafID)
	require.NoError(t, err)
	assert.Empty(t, tail)
	assert.Equal(t, leafID, currentLeafID)

	_, _, err = session.GetEntries(ctx, "missing-entry")
	require.ErrorIs(t, err, ErrInvalidCursor)
	require.NoError(t, session.Abort(ctx))

	confirmed := true
	require.NoError(t, session.RespondUI("dialog-1", UIResolution{Confirmed: &confirmed}))
	_, err = session.GetState(ctx)
	require.NoError(t, err)
	require.Error(t, session.Prompt(ctx, "not sent", "invalid"))

	require.NoError(t, session.Close(ctx))
}

func TestSessionAbortSlowStream(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "slow_stream")
	cfg := newSessionTestConfig(t, "s-20260802-abort1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)
	require.NoError(t, session.Prompt(ctx, "Start slowly", ""))

	for receiveSessionEvent(t, session.Events()).Type != "message_update" {
	}
	require.NoError(t, session.Abort(ctx))
	for receiveSessionEvent(t, session.Events()).Type != "agent_settled" {
	}

	entries, _, err := session.GetEntries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var assistant struct {
		Message struct {
			StopReason string `json:"stopReason"`
		} `json:"message"`
	}
	require.NoError(t, json.Unmarshal(entries[1], &assistant))
	assert.Equal(t, "aborted", assistant.Message.StopReason)
}

func TestSessionCrashFailsPendingAndCompletesInOrder(t *testing.T) {
	session := startLifecycleProcess(t, `printf '{"type":"message_update","delta":"partial"}\n'; IFS= read -r line; echo 'crash diagnostic' >&2; exit 7`)
	result := make(chan error, 1)
	go func() {
		_, err := session.GetState(context.Background())
		result <- err
	}()

	require.ErrorIs(t, <-result, ErrProcessExited)
	var events []Event
	for event := range session.Events() {
		events = append(events, event)
	}
	require.Len(t, events, 1)
	assert.Equal(t, "message_update", events[0].Type)
	select {
	case <-session.Done():
	default:
		t.Fatal("events closed before Done")
	}
	require.Error(t, session.ExitErr())
	_, err := session.stderr.Stat()
	require.Error(t, err)
	stderr, err := os.ReadFile(session.stderr.Name())
	require.NoError(t, err)
	assert.Contains(t, string(stderr), "crash diagnostic")
}

func TestSessionCloseIsConcurrentAndIdempotent(t *testing.T) {
	session := startLifecycleProcess(t, `trap 'exit 0' TERM; printf '{"type":"ready"}\n'; while :; do sleep 0.01; done`)
	assert.Equal(t, "ready", receiveSessionEvent(t, session.Events()).Type)

	const callers = 24
	errs := make(chan error, callers)
	var start sync.WaitGroup
	start.Add(1)
	for range callers {
		go func() {
			start.Wait()
			errs <- session.Close(context.Background())
		}()
	}
	start.Done()
	for range callers {
		require.NoError(t, <-errs)
	}
	require.NoError(t, session.Close(context.Background()))
	select {
	case <-session.Done():
	default:
		t.Fatal("concurrent Close returned before reap")
	}
}

func TestSessionCloseEscalatesAfterGrace(t *testing.T) {
	session := startLifecycleProcess(t, `trap '' TERM; printf '{"type":"ready"}\n'; while :; do :; done`)
	session.shutdownGrace = 30 * time.Millisecond
	assert.Equal(t, "ready", receiveSessionEvent(t, session.Events()).Type)

	started := time.Now()
	require.NoError(t, session.Close(context.Background()))
	assert.GreaterOrEqual(t, time.Since(started), 20*time.Millisecond)
	assert.Less(t, time.Since(started), time.Second)
	var exitErr *exec.ExitError
	require.ErrorAs(t, session.ExitErr(), &exitErr)
	status := exitErr.Sys().(syscall.WaitStatus)
	assert.True(t, status.Signaled())
	assert.Equal(t, syscall.SIGKILL, status.Signal())
}

func TestSessionForcedCloseKillsDetachedDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "detached.pid")
	script := fmt.Sprintf(
		`GIBSON_TEST_DETACHED_HELPER=1 GIBSON_TEST_DETACHED_PID_FILE=%q %q -test.run '^TestSessionDetachedProcessHelper$' & while [ ! -s %q ]; do sleep 0.01; done; printf '{"type":"ready"}\n'; trap '' TERM; while :; do sleep 0.01; done`,
		pidFile,
		os.Args[0],
		pidFile,
	)
	session := startLifecycleProcess(t, script)
	assert.Equal(t, "ready", receiveSessionEvent(t, session.Events()).Type)
	detachedPID := readProcessID(t, pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, session.Close(ctx))
	require.Eventually(t, func() bool {
		return !processExists(detachedPID)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestSessionUnexpectedExitKillsDetachedDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "stdout-holder.pid")
	script := fmt.Sprintf(
		`GIBSON_TEST_DETACHED_HELPER=1 GIBSON_TEST_DETACHED_PID_FILE=%q %q -test.run '^TestSessionDetachedProcessHelper$' & while [ ! -s %q ]; do sleep 0.01; done; sleep 0.1; printf '{"type":"final"}\n'; exit 7`,
		pidFile,
		os.Args[0],
		pidFile,
	)
	session := startLifecycleProcess(t, script)
	assert.Equal(t, "final", receiveSessionEvent(t, session.Events()).Type)
	detachedPID := readProcessID(t, pidFile)

	select {
	case <-session.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not reap after parent exit")
	}
	for range session.Events() {
	}
	require.Error(t, session.ExitErr())
	require.Eventually(t, func() bool {
		return !processExists(detachedPID)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestSessionReapsWhenStdoutDescriptorRemainsOpen(t *testing.T) {
	session, heldStdout := startLifecycleProcessHoldingStdout(t, `printf '{"type":"final"}\n'; exit 7`)
	t.Cleanup(func() { _ = heldStdout.Close() })

	assert.Equal(t, "final", receiveSessionEvent(t, session.Events()).Type)
	select {
	case <-session.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not reap with an open stdout descriptor")
	}
	for range session.Events() {
	}
	require.Error(t, session.ExitErr())
}

func TestSessionDetachedProcessHelper(t *testing.T) {
	if os.Getenv("GIBSON_TEST_DETACHED_HELPER") != "1" {
		return
	}
	if err := syscall.Setpgid(0, 0); err != nil {
		os.Exit(2)
	}
	pidFile := os.Getenv("GIBSON_TEST_DETACHED_PID_FILE")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600); err != nil {
		os.Exit(3)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestSessionCloseEscalatesAndUnblocksFullEvents(t *testing.T) {
	var script bytes.Buffer
	script.WriteString(`trap '' TERM; i=0; while [ "$i" -lt 300 ]; do printf '{"type":"event","index":%s}\n' "$i"; i=$((i+1)); done; while :; do :; done`)
	session := startLifecycleProcess(t, script.String())
	session.shutdownGrace = 30 * time.Millisecond
	require.Eventually(t, func() bool {
		return len(session.rpc.events) == eventBufferSize
	}, 5*time.Second, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	require.NoError(t, session.Close(ctx))
	assert.Less(t, time.Since(started), time.Second)
	var exitErr *exec.ExitError
	require.ErrorAs(t, session.ExitErr(), &exitErr)
	status := exitErr.Sys().(syscall.WaitStatus)
	assert.True(t, status.Signaled())
	assert.Equal(t, syscall.SIGKILL, status.Signal())
	for range session.Events() {
	}
}

func TestSpawnCleansUpAfterReadinessFailure(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "missing-scenario")
	cfg := newSessionTestConfig(t, "s-20260728-failed")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Contains(t, err.Error(), "probe pi readiness")

	stderr, readErr := os.ReadFile(cfg.StderrPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(stderr), `unsupported FAKEPI_SCENARIO "missing-scenario"`)
	require.NoError(t, os.Remove(cfg.StderrPath))
}

func startLifecycleProcess(t testing.TB, script string) *Session {
	t.Helper()
	session, childStdout := startLifecycleProcessHoldingStdout(t, script)
	require.NoError(t, childStdout.Close())
	return session
}

func startLifecycleProcessHoldingStdout(t testing.TB, script string) (*Session, *os.File) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, childStdout, err := os.Pipe()
	require.NoError(t, err)
	cmd.Stdout = childStdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr.log"))
	require.NoError(t, err)
	cmd.Stderr = stderr
	require.NoError(t, cmd.Start())

	logger := log.New(bytes.NewBuffer(nil))
	processes := newOwnedProcessTracker(cmd.Process.Pid)
	session := &Session{
		id:               "lifecycle-test",
		pid:              cmd.Process.Pid,
		cmd:              cmd,
		rpc:              newRPCClient(stdout, stdin, logger),
		stdout:           stdout,
		stderr:           stderr,
		logger:           logger,
		processes:        processes,
		done:             make(chan struct{}),
		complete:         make(chan struct{}),
		shutdownDone:     make(chan struct{}),
		shutdownEscalate: make(chan struct{}),
		shutdownGrace:    200 * time.Millisecond,
	}
	go processes.run()
	go session.reap()
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = session.Close(ctx)
	})
	return session, childStdout
}

func readProcessID(t testing.TB, path string) int {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var pid int
	_, err = fmt.Sscanf(string(contents), "%d", &pid)
	require.NoError(t, err)
	return pid
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func receiveSessionEvent(t testing.TB, events <-chan Event) Event {
	t.Helper()
	select {
	case event, ok := <-events:
		require.True(t, ok, "session event channel closed prematurely")
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("session event was not received")
		return Event{}
	}
}

func newSessionTestConfig(t testing.TB, id string) Config {
	t.Helper()
	cwd := t.TempDir()
	return Config{
		PiBin:      pitest.BuildFakePi(t),
		SessionID:  id,
		SessionDir: filepath.Join(cwd, ".gibson", "sessions"),
		Cwd:        cwd,
		StderrPath: filepath.Join(cwd, ".gibson", "logs", id+".stderr.log"),
	}
}

func cleanupSession(t testing.TB, session *Session) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := session.Close(ctx); err == nil {
			return
		}
		_ = session.cmd.Process.Kill()
		<-session.complete
	})
}

func assertSessionFileMatchesEntries(t testing.TB, path string, cfg Config, entries []json.RawMessage) {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(contents), []byte{'\n'})
	require.Len(t, lines, len(entries)+1)

	var header struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
	}
	require.NoError(t, json.Unmarshal(lines[0], &header))
	assert.Equal(t, "session", header.Type)
	assert.Equal(t, 3, header.Version)
	assert.Equal(t, cfg.SessionID, header.ID)
	assert.Equal(t, cfg.Cwd, header.Cwd)
	for index, entry := range entries {
		assert.Equal(t, string(lines[index+1]), string(entry))
	}
}
