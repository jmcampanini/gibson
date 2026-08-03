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
	"strings"
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
	require.NoError(t, os.MkdirAll(filepath.Dir(cfg.StderrPath), 0o700))
	require.NoError(t, os.Chmod(filepath.Dir(cfg.StderrPath), 0o700))
	require.NoError(t, os.WriteFile(cfg.StderrPath, nil, 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)

	stderrDirInfo, err := os.Stat(filepath.Dir(cfg.StderrPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), stderrDirInfo.Mode().Perm())
	stderrInfo, err := os.Stat(cfg.StderrPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), stderrInfo.Mode().Perm())
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

func TestSessionHugeEntrySurvivesProcessBoundary(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "huge_entry")
	cfg := newSessionTestConfig(t, "s-20260803-hostile1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)

	stateRaw, err := session.GetState(ctx)
	require.NoError(t, err)
	var state struct {
		SessionFile string `json:"sessionFile"`
	}
	require.NoError(t, json.Unmarshal(stateRaw, &state))
	require.NoError(t, session.Prompt(ctx, "Exercise hostile records", ""))

	var toolUpdates int
	var unicodeRaw json.RawMessage
	seen := make(map[string]bool)
	for {
		event := receiveSessionEvent(t, session.Events())
		seen[event.Type] = true
		if event.Type == "tool_execution_update" {
			toolUpdates++
		}
		if event.Type == "message_update" {
			unicodeRaw = append(json.RawMessage(nil), event.Raw...)
		}
		if event.Type == "agent_settled" {
			break
		}
	}
	assert.Equal(t, 1024, toolUpdates)
	for _, eventType := range []string{
		"tool_execution_start", "tool_execution_end", "extension_ui_request", "extension_error", "agent_settled",
	} {
		assert.True(t, seen[eventType], "missing %s", eventType)
	}
	require.Contains(t, string(unicodeRaw), "\u2028")
	require.Contains(t, string(unicodeRaw), "\u2029")
	assert.NotContains(t, string(unicodeRaw), `\u2028`)
	assert.NotContains(t, string(unicodeRaw), `\u2029`)

	entries, leafID, err := session.GetEntries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.NotEmpty(t, leafID)
	var hugeRaw json.RawMessage
	for _, raw := range entries {
		var head struct {
			CustomType string `json:"customType"`
		}
		require.NoError(t, json.Unmarshal(raw, &head))
		if head.CustomType == "gibson-hostile-record" {
			hugeRaw = raw
		}
	}
	require.Greater(t, len(hugeRaw), 1<<20)
	var huge struct {
		Data struct {
			Marker  string `json:"marker"`
			Payload string `json:"payload"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(hugeRaw, &huge))
	assert.Equal(t, "huge-entry", huge.Data.Marker)
	assert.Len(t, huge.Data.Payload, (1<<20)+1)
	assert.Empty(t, strings.Trim(huge.Data.Payload, "x"))
	assertSessionFileMatchesEntries(t, state.SessionFile, cfg, entries)
}

func TestSessionRespondUIReleasesBlockedDialogAmidConcurrentCommands(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "dialog_confirm")
	cfg := newSessionTestConfig(t, "s-20260803-dialog1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)
	acceptance, err := session.StartPrompt(ctx, "Ask first", "")
	require.NoError(t, err)

	var dialog Event
	for dialog.Type != "extension_ui_request" {
		dialog = receiveSessionEvent(t, session.Events())
	}
	var request struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	require.NoError(t, json.Unmarshal(dialog.Raw, &request))
	assert.Equal(t, "fp-d-1", request.ID)
	assert.Equal(t, "confirm", request.Method)
	select {
	case err := <-acceptance:
		t.Fatalf("prompt resolved before dialog response: %v", err)
	default:
	}

	start := make(chan struct{})
	stateResult := make(chan error, 1)
	responseResult := make(chan error, 1)
	go func() {
		<-start
		_, err := session.GetState(ctx)
		stateResult <- err
	}()
	go func() {
		<-start
		confirmed := true
		responseResult <- session.RespondUI(request.ID, UIResolution{Confirmed: &confirmed})
	}()
	close(start)
	require.NoError(t, <-responseResult)
	require.NoError(t, <-stateResult)
	require.NoError(t, <-acceptance)

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
	assert.Equal(t, "stop", assistant.Message.StopReason)
}

func TestSessionPreservesCRLFUnicodeAndFinalUnterminatedRecord(t *testing.T) {
	finalRaw := []byte("{\"type\":\"final\",\"value\":\"left\u2028middle\u2029right\"}")
	script := `printf '{"type":"crlf","value":"kept"}\r\n'; printf '%s' '` + string(finalRaw) + `'; exit 7`
	session := startLifecycleProcess(t, script)

	var events []Event
	for event := range session.Events() {
		events = append(events, event)
	}
	require.Len(t, events, 2)
	assert.Equal(t, `{"type":"crlf","value":"kept"}`, string(events[0].Raw))
	assert.Equal(t, finalRaw, []byte(events[1].Raw))
	select {
	case <-session.Done():
	default:
		t.Fatal("events closed before process completion")
	}
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

	require.ErrorIs(t, <-result, ErrTransportClosed)
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

func TestOwnedProcessTrackerRejectsReusedRootAndDescendantPIDs(t *testing.T) {
	records := []processRecord{
		{pid: 100, ppid: 1, pgid: 100, started: "root-original"},
		{pid: 101, ppid: 100, pgid: 100, started: "child-original"},
	}
	list := func() ([]processRecord, error) {
		return append([]processRecord(nil), records...), nil
	}
	read := func(pid int) (processRecord, bool, error) {
		for _, record := range records {
			if record.pid == pid {
				return record, true, nil
			}
		}
		return processRecord{}, false, nil
	}
	tracker, err := newOwnedProcessTrackerWith(100, list, read)
	require.NoError(t, err)
	require.NoError(t, tracker.capture())
	require.Contains(t, tracker.owned, 101)

	records = []processRecord{
		{pid: 100, ppid: 1, pgid: 100, started: "root-reused"},
		{pid: 101, ppid: 200, pgid: 200, started: "child-reused"},
		{pid: 102, ppid: 100, pgid: 100, started: "unrelated-child"},
	}
	require.NoError(t, tracker.capture())
	assert.True(t, tracker.rootGone)
	assert.NotContains(t, tracker.owned, 101)
	assert.NotContains(t, tracker.owned, 102)
}

func TestOwnedProcessTrackerRejectsMixedSnapshotAncestry(t *testing.T) {
	snapshot := []processRecord{
		{pid: 100, ppid: 1, pgid: 100, started: "root"},
		{pid: 101, ppid: 100, pgid: 100, started: "parent-before-reuse"},
		{pid: 102, ppid: 101, pgid: 101, started: "unrelated-child"},
	}
	live := map[int]processRecord{
		100: {pid: 100, ppid: 1, pgid: 100, started: "root"},
		101: {pid: 101, ppid: 200, pgid: 200, started: "parent-after-reuse"},
		102: {pid: 102, ppid: 101, pgid: 101, started: "unrelated-child"},
	}
	list := func() ([]processRecord, error) {
		return append([]processRecord(nil), snapshot...), nil
	}
	read := func(pid int) (processRecord, bool, error) {
		record, exists := live[pid]
		return record, exists, nil
	}
	tracker, err := newOwnedProcessTrackerWith(100, list, read)
	require.NoError(t, err)
	require.NoError(t, tracker.capture())
	assert.Empty(t, tracker.owned)
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

func TestSessionUnexpectedExitKillsReparentedDescendantAfterProcessGroupChange(t *testing.T) {
	stateDir := t.TempDir()
	pidFile := filepath.Join(stateDir, "transition.pid")
	gateFile := filepath.Join(stateDir, "detach-now")
	detachedFile := filepath.Join(stateDir, "detached")
	script := fmt.Sprintf(
		`GIBSON_TEST_DETACHED_HELPER=1 GIBSON_TEST_DETACHED_PID_FILE=%q GIBSON_TEST_DETACH_GATE=%q GIBSON_TEST_DETACHED_READY=%q %q -test.run '^TestSessionDetachedProcessHelper$' & while [ ! -s %q ]; do sleep 0.01; done; printf '{"type":"ready"}\n'; while [ ! -s %q ]; do sleep 0.01; done; printf '{"type":"final"}\n'; exit 7`,
		pidFile,
		gateFile,
		detachedFile,
		os.Args[0],
		pidFile,
		detachedFile,
	)
	session := startLifecycleProcess(t, script)
	assert.Equal(t, "ready", receiveSessionEvent(t, session.Events()).Type)
	detachedPID := readProcessID(t, pidFile)
	t.Cleanup(func() { _ = signalProcessGroup(detachedPID, syscall.SIGKILL) })
	require.NoError(t, session.processes.capture())
	session.processes.stop()
	require.NoError(t, os.WriteFile(gateFile, []byte("detach"), 0o600))
	assert.Equal(t, "final", receiveSessionEvent(t, session.Events()).Type)

	select {
	case <-session.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not reap after descendant changed process group")
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
	pidFile := os.Getenv("GIBSON_TEST_DETACHED_PID_FILE")
	gateFile := os.Getenv("GIBSON_TEST_DETACH_GATE")
	if gateFile != "" {
		if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600); err != nil {
			os.Exit(2)
		}
		for {
			if _, err := os.Stat(gateFile); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := syscall.Setpgid(0, 0); err != nil {
		os.Exit(3)
	}
	if gateFile == "" {
		if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
	} else if err := os.WriteFile(os.Getenv("GIBSON_TEST_DETACHED_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(5)
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
	processes, err := newOwnedProcessTracker(cmd.Process.Pid)
	require.NoError(t, err)
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
