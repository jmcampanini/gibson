package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/internal/config"
	"github.com/jmcampanini/gibson/internal/pisession"
	"github.com/jmcampanini/gibson/internal/pitest"
	"github.com/jmcampanini/gibson/internal/store"
	"github.com/jmcampanini/gibson/internal/testws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCompletesOneShotWorkflowWithFakePi(t *testing.T) {
	piBin := pitest.BuildFakePi(t)
	ws := testws.New(t,
		testws.WithPiBin(piBin),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick task"}),
	)
	nested := filepath.Join(ws.Checkout, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))

	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return nested, nil }
	dependencies.resolvePiBin = pisession.ResolvePiBin
	dependencies.checkPiVersion = pisession.CheckPiVersion
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	outcome, err := run(context.Background(), RunOptions{
		Type:    "quick",
		Message: "Say hello",
		Stdout:  &stdout,
		Stderr:  &stderr,
	}, log.New(&stderr), dependencies)

	require.NoError(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Equal(t, "Hello from fake pi.\n", stdout.String())
	assert.Contains(t, stderr.String(), "[session] id=")
	assert.Contains(t, stderr.String(), "status=stopped")
	assert.NotContains(t, stdout.String(), "[session]")

	registry := readRunRegistry(t, ws.Checkout)
	assert.Equal(t, 1, registry.Version)
	require.Len(t, registry.Sessions, 1)
	var record store.Record
	for _, candidate := range registry.Sessions {
		record = candidate
	}
	assert.Equal(t, "quick", record.Type)
	assert.Equal(t, store.StatusStopped, record.Status)
	assert.Zero(t, record.PID)
	assert.NotEmpty(t, record.CreatedAt)
	assert.NotEmpty(t, record.LastActivityAt)

	sessionFiles, err := filepath.Glob(filepath.Join(ws.Checkout, ".gibson", "sessions", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, sessionFiles, 1)
	contents, err := os.ReadFile(sessionFiles[0])
	require.NoError(t, err)
	firstLine, _, _ := bytes.Cut(contents, []byte{'\n'})
	var header struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(firstLine, &header))
	assert.Equal(t, record.ID, header.ID)
	_, err = os.Stat(filepath.Join(ws.Checkout, ".gibson", "logs", record.ID+".stderr.log"))
	require.NoError(t, err)

	gitStatus := exec.Command("git", "-C", ws.Checkout, "status", "--porcelain")
	status, err := gitStatus.Output()
	require.NoError(t, err)
	assert.Empty(t, status)
}

func TestRunTargetsNamedCheckoutWithIsolatedArtifacts(t *testing.T) {
	piBin := pitest.BuildFakePi(t)
	ws := testws.New(t,
		testws.WithPiBin(piBin),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick task"}),
		testws.WithSiblingCheckout("wt-x"),
	)
	target := filepath.Join(ws.Root, "wt-x")
	physicalTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.resolvePiBin = pisession.ResolvePiBin
	dependencies.checkPiVersion = pisession.CheckPiVersion
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	outcome, err := run(context.Background(), RunOptions{
		Type: "quick", Message: "Say hello", Checkout: "wt-x", Stdout: &stdout, Stderr: &stderr,
	}, log.New(&stderr), dependencies)

	require.NoError(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Equal(t, "Hello from fake pi.\n", stdout.String())
	_, err = os.Stat(filepath.Join(ws.Checkout, ".gibson"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	registry := readRunRegistry(t, target)
	require.Len(t, registry.Sessions, 1)
	var record store.Record
	for _, candidate := range registry.Sessions {
		record = candidate
	}
	assert.Equal(t, store.StatusStopped, record.Status)
	assert.Zero(t, record.PID)
	sessionPath, err := store.Open(target).FindSessionFile(record.ID)
	require.NoError(t, err)
	contents, err := os.ReadFile(sessionPath)
	require.NoError(t, err)
	firstLine, _, _ := bytes.Cut(contents, []byte{'\n'})
	var header struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	}
	require.NoError(t, json.Unmarshal(firstLine, &header))
	assert.Equal(t, record.ID, header.ID)
	assert.Equal(t, physicalTarget, header.CWD)
	assert.FileExists(t, filepath.Join(target, ".gibson", "logs", record.ID+".stderr.log"))
	for _, checkout := range []string{ws.Checkout, target} {
		status, err := exec.Command("git", "-C", checkout, "status", "--porcelain").Output()
		require.NoError(t, err)
		assert.Empty(t, status)
	}
}

func TestRunRejectsInvalidCheckoutBeforePiResolutionOrSpawn(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	resolvedPi := false
	spawned := false
	dependencies.resolvePiBin = func(string) (string, error) {
		resolvedPi = true
		return "pi", nil
	}
	dependencies.spawn = func(context.Context, pisession.Config) (runSession, error) {
		spawned = true
		return nil, errors.New("unexpected spawn")
	}

	outcome, err := run(context.Background(), RunOptions{
		Type: "quick", Message: "hello", Checkout: "../outside",
	}, log.New(&bytes.Buffer{}), dependencies)

	require.Error(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.ErrorContains(t, err, "not a valid checkout name")
	assert.False(t, resolvedPi)
	assert.False(t, spawned)
	_, statErr := os.Stat(filepath.Join(ws.Checkout, ".gibson"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestRunBufferedInterruptCannotOvertakeFakePiPrompt(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "slow_stream")
	piBin := pitest.BuildFakePi(t)
	ws := testws.New(t,
		testws.WithPiBin(piBin),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick task"}),
	)
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.resolvePiBin = pisession.ResolvePiBin
	dependencies.checkPiVersion = pisession.CheckPiVersion
	interrupts := make(chan os.Signal, 2)
	interrupts <- os.Interrupt
	dependencies.interrupts = interrupts
	stdout := newObservedBuffer()
	var stderr bytes.Buffer
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{
			Type: "quick", Message: "stream", Stdout: stdout, Stderr: &stderr,
		}, log.New(&stderr), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish durable abort")
	}
	assert.NotContains(t, stdout.String(), "deterministic deltas")
	assert.Contains(t, stderr.String(), "status=stopped")

	sessionFiles, err := filepath.Glob(filepath.Join(ws.Checkout, ".gibson", "sessions", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, sessionFiles, 1)
	assert.Equal(t, []string{"aborted"}, assistantStopReasons(t, sessionFiles[0]))
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunCrashWithFakePiReportsStderrAndCleansRegistry(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "crash_mid_stream")
	piBin := pitest.BuildFakePi(t)
	ws := testws.New(t,
		testws.WithPiBin(piBin),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick task"}),
	)
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.resolvePiBin = pisession.ResolvePiBin
	dependencies.checkPiVersion = pisession.CheckPiVersion
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	outcome, err := run(context.Background(), RunOptions{
		Type: "quick", Message: "crash", Stdout: &stdout, Stderr: &stderr,
	}, log.New(&stderr), dependencies)

	require.Error(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Equal(t, "Partial output before crash.\n", stdout.String())
	assert.Contains(t, stderr.String(), "deterministic crash after first delta")
	assert.Contains(t, stderr.String(), "stderr_log")
	assert.Contains(t, stderr.String(), "status=stopped")
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunRoutesHostileFakePiRecordsAfterConsumerBackpressure(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "huge_entry")
	piBin := pitest.BuildFakePi(t)
	ws := testws.New(t,
		testws.WithPiBin(piBin),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick task"}),
	)
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.resolvePiBin = pisession.ResolvePiBin
	dependencies.checkPiVersion = pisession.CheckPiVersion
	spawned := make(chan *pisession.Session, 1)
	dependencies.spawn = func(ctx context.Context, cfg pisession.Config) (runSession, error) {
		session, err := pisession.Spawn(ctx, cfg)
		if err == nil {
			spawned <- session
		}
		return session, err
	}
	stderr := newBlockingDiagnosticWriter("[tool bash] running")
	t.Cleanup(stderr.release)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(ctx, RunOptions{
			Type: "quick", Message: "Exercise hostile records", Stdout: &stdout, Stderr: stderr,
		}, log.New(stderr), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	var session *pisession.Session
	select {
	case session = <-spawned:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not expose its spawned pi session")
	}
	waitRunSignal(t, stderr.blocked, "blocked tool diagnostic")
	waitRunEventChannelFull(t, session.Events())
	select {
	case got := <-finished:
		t.Fatalf("run bypassed application backpressure: outcome=%v err=%v", got.outcome, got.err)
	default:
	}
	stderr.release()

	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunCompleted, got.outcome)
	case <-time.After(10 * time.Second):
		t.Fatal("hostile run did not finish after backpressure was released")
	}
	assert.Equal(t, "Unicode: left\u2028middle\u2029right.\n", stdout.String())
	assert.NotContains(t, stdout.String(), "[tool")
	assert.NotContains(t, stdout.String(), "[notify]")
	assert.NotContains(t, stdout.String(), "[error]")
	assert.Contains(t, stderr.String(), "[tool bash] running")
	assert.Contains(t, stderr.String(), "[tool bash] done")
	assert.Contains(t, stderr.String(), "[notify] hostile record notification")
	assert.Contains(t, stderr.String(), "[error] extension hostile-extension.ts: deterministic extension failure")

	files, err := filepath.Glob(filepath.Join(ws.Checkout, ".gibson", "sessions", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	sessionFile, err := os.Stat(files[0])
	require.NoError(t, err)
	assert.Greater(t, sessionFile.Size(), int64(1<<20))
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunRejectsUnknownTypeBeforeSpawningPi(t *testing.T) {
	ws := testws.New(t,
		testws.WithSessionType("review", config.SessionType{Description: "Review"}),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick"}),
	)
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	spawned := false
	dependencies.spawn = func(context.Context, pisession.Config) (runSession, error) {
		spawned = true
		return nil, errors.New("unexpected spawn")
	}

	outcome, err := run(context.Background(), RunOptions{Type: "missing", Message: "hello"}, log.New(&bytes.Buffer{}), dependencies)

	require.Error(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Contains(t, err.Error(), `unknown session type "missing"`)
	assert.Contains(t, err.Error(), "quick, review, test")
	assert.False(t, spawned)
	_, statErr := os.Stat(filepath.Join(ws.Checkout, ".gibson"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestReadFileTailReturnsBoundedUTF8Suffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr.log")
	require.NoError(t, os.WriteFile(path, []byte("prefix-\xfftail"), 0o600))

	tail, truncated, err := readFileTail(path, 8)

	require.NoError(t, err)
	assert.True(t, truncated)
	assert.LessOrEqual(t, len(tail), 8)
	assert.Contains(t, tail, "tail")
	assert.True(t, strings.ToValidUTF8(tail, "") == tail)
}

func TestRunPresenterKeepsAssistantTextSeparateFromDiagnostics(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	presenter := runPresenter{stdout: &stdout, stderr: &stderr}
	events := []pisession.Event{
		runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"private"}}`),
		runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Answer"}}`),
		runEvent("tool_execution_start", `{"type":"tool_execution_start","toolName":"bash"}`),
		runEvent("tool_execution_end", `{"type":"tool_execution_end","toolName":"bash","isError":false}`),
		runEvent("extension_ui_request", `{"type":"extension_ui_request","method":"notify","message":"notice"}`),
		runEvent("extension_ui_request", `{"type":"extension_ui_request","method":"confirm","message":"continue?"}`),
		runEvent("extension_error", `{"type":"extension_error","extensionPath":"review.ts","error":"failed"}`),
	}
	for _, event := range events {
		require.NoError(t, presenter.present(event))
	}
	require.NoError(t, presenter.finishText())

	assert.Equal(t, "Answer\n", stdout.String())
	assert.NotContains(t, stdout.String(), "private")
	assert.Contains(t, stderr.String(), "[tool bash] running")
	assert.Contains(t, stderr.String(), "[tool bash] done")
	assert.Contains(t, stderr.String(), "[notify] notice")
	assert.Contains(t, stderr.String(), "cannot answer dialogs")
	assert.Contains(t, stderr.String(), "extension review.ts: failed")
}

func TestRunWarnsAndDurablyAbortsBlockedFakePiDialog(t *testing.T) {
	t.Setenv("FAKEPI_SCENARIO", "dialog_confirm")
	piBin := pitest.BuildFakePi(t)
	ws := testws.New(t,
		testws.WithPiBin(piBin),
		testws.WithSessionType("quick", config.SessionType{Description: "Quick"}),
	)
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.resolvePiBin = pisession.ResolvePiBin
	dependencies.checkPiVersion = pisession.CheckPiVersion
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	stderr := newObservedDiagnosticWriter("cannot answer dialogs")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(ctx, RunOptions{
			Type: "quick", Message: "/dialog", Stdout: &stdout, Stderr: stderr,
		}, log.New(stderr), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, stderr.observed, "blocking dialog warning")
	interrupts <- os.Interrupt
	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(10 * time.Second):
		t.Fatal("dialog run did not finish durable abort")
	}

	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "[warning] pi is waiting for a confirm dialog; gibson run cannot answer dialogs")
	assert.Contains(t, stderr.String(), "status=stopped")
	sessionFiles, err := filepath.Glob(filepath.Join(ws.Checkout, ".gibson", "sessions", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, sessionFiles, 1)
	assert.Equal(t, []string{"aborted"}, assistantStopReasons(t, sessionFiles[0]))
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunFinishesPromptHandledWithoutAgentRun(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		return newScriptedRunSession(cfg, make(chan pisession.Event)), nil
	}

	outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "/handled"}, log.New(&bytes.Buffer{}), dependencies)

	require.NoError(t, err)
	assert.Equal(t, RunCompleted, outcome)
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunInterruptFinishesPromptHandledWithoutAgentRun(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	interrupts <- os.Interrupt
	dependencies.interrupts = interrupts
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		return newScriptedRunSession(cfg, make(chan pisession.Event)), nil
	}

	outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "/handled"}, log.New(&bytes.Buffer{}), dependencies)

	require.NoError(t, err)
	assert.Equal(t, RunInterrupted, outcome)
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunRejectsAmbiguousIdleStateFromUnverifiedPi(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.checkPiVersion = func(context.Context, string) (pisession.VersionResult, error) {
		return pisession.VersionResult{Found: "0.83.0", Verified: false}, nil
	}
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		return newScriptedRunSession(cfg, make(chan pisession.Event)), nil
	}

	outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "/handled"}, log.New(&bytes.Buffer{}), dependencies)

	require.Error(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Contains(t, err.Error(), "cannot safely determine prompt completion with unverified pi 0.83.0")
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunWaitsForAgentSettledAfterPiReportsIdle(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	stateObserved := make(chan struct{})
	events := make(chan pisession.Event, 1)
	events <- runEvent("agent_start", `{"type":"agent_start"}`)
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, events)
		stateCalls := 0
		session.getState = func() (json.RawMessage, error) {
			stateCalls++
			if stateCalls == 2 {
				close(stateObserved)
			}
			return session.state, nil
		}
		return session, nil
	}
	type runResult struct {
		outcome RunOutcome
		err     error
	}
	result := make(chan runResult, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "start"}, log.New(&bytes.Buffer{}), dependencies)
		result <- runResult{outcome: outcome, err: err}
	}()

	select {
	case <-stateObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not check post-prompt state")
	}
	select {
	case got := <-result:
		t.Fatalf("run returned before agent_settled: outcome=%v err=%v", got.outcome, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	events <- runEvent("agent_settled", `{"type":"agent_settled"}`)

	select {
	case got := <-result:
		require.NoError(t, got.err)
		assert.Equal(t, RunCompleted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after agent_settled")
	}
}

func TestRunPrioritizesBufferedInterruptOverQueuedSettlement(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	stateObserved := make(chan struct{})
	events := make(chan pisession.Event, 2)
	events <- runEvent("agent_start", `{"type":"agent_start"}`)
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, events)
		stateCalls := 0
		session.getState = func() (json.RawMessage, error) {
			stateCalls++
			if stateCalls == 2 {
				close(stateObserved)
			}
			return session.state, nil
		}
		return session, nil
	}
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "start"}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, stateObserved, "post-prompt idle state")
	interrupts <- os.Interrupt
	events <- runEvent("agent_settled", `{"type":"agent_settled"}`)
	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("queued settlement won over buffered interrupt")
	}
}

func TestRunAllowsAgentStartAfterPromptAcceptance(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		events := make(chan pisession.Event, 4)
		stateChecked := make(chan struct{})
		session := newScriptedRunSession(cfg, events)
		stateCalls := 0
		session.getState = func() (json.RawMessage, error) {
			stateCalls++
			if stateCalls == 2 {
				close(stateChecked)
				return bytes.Replace(session.state, []byte("false"), []byte("true"), 1), nil
			}
			return session.state, nil
		}
		session.onPrompt = func() {
			go func() {
				<-stateChecked
				events <- runEvent("agent_start", `{"type":"agent_start"}`)
				events <- runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"late"}}`)
				events <- runEvent("message_end", `{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}`)
				events <- runEvent("agent_settled", `{"type":"agent_settled"}`)
			}()
		}
		return session, nil
	}
	var stdout bytes.Buffer

	outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "start", Stdout: &stdout}, log.New(&bytes.Buffer{}), dependencies)

	require.NoError(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Equal(t, "late\n", stdout.String())
}

func TestRunFailsWhenFinalAssistantReportsError(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		events := make(chan pisession.Event, 2)
		events <- runEvent("message_end", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"provider unavailable"}}`)
		events <- runEvent("agent_settled", `{"type":"agent_settled"}`)
		return &scriptedRunSession{
			pid:    43100,
			events: events,
			state: json.RawMessage(fmt.Sprintf(
				`{"sessionId":%q,"sessionFile":%q}`,
				cfg.SessionID,
				filepath.Join(cfg.SessionDir, "opaque.jsonl"),
			)),
		}, nil
	}

	outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "fail"}, log.New(&bytes.Buffer{}), dependencies)

	require.Error(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Contains(t, err.Error(), "assistant response failed: provider unavailable")
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunInterruptClosesWithFreshContextAndStopsRegistryRecord(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	prompted := make(chan struct{})
	closed := make(chan bool, 1)
	var session *scriptedRunSession
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session = &scriptedRunSession{
			pid:      43210,
			events:   make(chan pisession.Event),
			prompted: prompted,
			closed:   closed,
			state: json.RawMessage(fmt.Sprintf(
				`{"sessionId":%q,"sessionFile":%q,"isStreaming":true}`,
				cfg.SessionID,
				filepath.Join(cfg.SessionDir, "opaque.jsonl"),
			)),
		}
		return session, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		outcome RunOutcome
		err     error
	}, 1)
	go func() {
		outcome, err := run(ctx, RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)
		result <- struct {
			outcome RunOutcome
			err     error
		}{outcome: outcome, err: err}
	}()

	select {
	case <-prompted:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not prompt")
	}
	cancel()

	select {
	case cleanupContextActive := <-closed:
		assert.True(t, cleanupContextActive)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not close its session")
	}
	select {
	case got := <-result:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after interruption")
	}

	registry := readRunRegistry(t, ws.Checkout)
	require.Len(t, registry.Sessions, 1)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunFirstInterruptAbortsThroughDurableSettlement(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	events := make(chan pisession.Event, 8)
	prompted := make(chan struct{})
	abortCalled := make(chan struct{})
	releaseAbort := make(chan struct{})
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, events)
		session.prompted = prompted
		session.state = bytes.Replace(session.state, []byte("false"), []byte("true"), 1)
		session.abort = func(context.Context) error {
			close(abortCalled)
			<-releaseAbort
			return nil
		}
		return session, nil
	}
	stdout := newObservedBuffer()
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{
			Type: "quick", Message: "wait", Stdout: stdout,
		}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, prompted, "prompt")
	events <- runEvent("agent_start", `{"type":"agent_start"}`)
	events <- runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"before"}}`)
	waitRunSignal(t, stdout.observed, "first assistant delta")
	interrupts <- os.Interrupt
	waitRunSignal(t, abortCalled, "abort command")
	events <- runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"after"}}`)
	events <- runEvent("message_end", `{"type":"message_end","message":{"role":"assistant","stopReason":"aborted"}}`)
	events <- runEvent("agent_end", `{"type":"agent_end","willRetry":false}`)
	events <- runEvent("agent_settled", `{"type":"agent_settled"}`)
	close(releaseAbort)

	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after aborted settlement")
	}
	assert.Equal(t, "before\n", stdout.String())
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunInterruptUsesSettlementObservedBeforePromptResult(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	events := make(chan pisession.Event)
	eventsSent := make(chan struct{})
	releasePrompt := make(chan struct{})
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, events)
		session.prompt = func(context.Context, string, string) error {
			events <- runEvent("agent_start", `{"type":"agent_start"}`)
			events <- runEvent("message_end", `{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}`)
			events <- runEvent("agent_settled", `{"type":"agent_settled"}`)
			close(eventsSent)
			<-releasePrompt
			return nil
		}
		session.close = func(context.Context) error {
			close(releasePrompt)
			return nil
		}
		return session, nil
	}
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, eventsSent, "pre-acceptance settlement")
	interrupts <- os.Interrupt
	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run ignored settlement observed before interrupt")
	}
}

func TestRunBufferedInterruptWinsImmediatePromptWriteFailure(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	interrupts <- os.Interrupt
	dependencies.interrupts = interrupts
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, make(chan pisession.Event))
		session.startPrompt = func(context.Context, string, string) (<-chan error, error) {
			return nil, pisession.ErrCommandTimeout
		}
		return session, nil
	}

	outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)

	require.ErrorIs(t, err, pisession.ErrCommandTimeout)
	assert.Equal(t, RunInterrupted, outcome)
}

func TestRunPromptWriteFailureAfterInterruptStaysInterrupted(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	promptWriteStarted := make(chan struct{})
	releasePromptWrite := make(chan struct{})
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, make(chan pisession.Event))
		session.startPrompt = func(context.Context, string, string) (<-chan error, error) {
			close(promptWriteStarted)
			<-releasePromptWrite
			return nil, pisession.ErrCommandTimeout
		}
		return session, nil
	}
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, promptWriteStarted, "blocked prompt write")
	interrupts <- os.Interrupt
	close(releasePromptWrite)
	select {
	case got := <-finished:
		require.ErrorIs(t, got.err, pisession.ErrCommandTimeout)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt write failure lost interrupt outcome")
	}
}

func TestRunSecondInterruptForcesBlockedPromptWrite(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	promptWriteStarted := make(chan struct{})
	releasePromptWrite := make(chan struct{})
	forcedClose := make(chan bool, 1)
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, make(chan pisession.Event))
		session.startPrompt = func(context.Context, string, string) (<-chan error, error) {
			close(promptWriteStarted)
			<-releasePromptWrite
			return nil, errors.New("transport closed")
		}
		session.close = func(ctx context.Context) error {
			forcedClose <- ctx.Err() != nil
			close(releasePromptWrite)
			return nil
		}
		return session, nil
	}
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, promptWriteStarted, "blocked prompt write")
	interrupts <- os.Interrupt
	interrupts <- os.Interrupt
	select {
	case forced := <-forcedClose:
		assert.True(t, forced)
	case <-time.After(5 * time.Second):
		t.Fatal("second interrupt did not force blocked prompt write")
	}
	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after forcing blocked prompt write")
	}
}

func TestRunSecondInterruptForcesShutdown(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 2)
	dependencies.interrupts = interrupts
	prompted := make(chan struct{})
	abortCalled := make(chan struct{})
	releaseAbort := make(chan struct{})
	closeStarted := make(chan struct{})
	forcedClose := make(chan bool, 1)
	events := make(chan pisession.Event, 4)
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, events)
		session.prompted = prompted
		session.state = bytes.Replace(session.state, []byte("false"), []byte("true"), 1)
		session.abort = func(context.Context) error {
			close(abortCalled)
			<-releaseAbort
			return nil
		}
		session.close = func(ctx context.Context) error {
			close(closeStarted)
			<-ctx.Done()
			forcedClose <- true
			return nil
		}
		return session, nil
	}
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, prompted, "prompt")
	events <- runEvent("agent_start", `{"type":"agent_start"}`)
	interrupts <- os.Interrupt
	waitRunSignal(t, abortCalled, "abort command")
	events <- runEvent("message_end", `{"type":"message_end","message":{"role":"assistant","stopReason":"aborted"}}`)
	events <- runEvent("agent_settled", `{"type":"agent_settled"}`)
	close(releaseAbort)
	waitRunSignal(t, closeStarted, "graceful close")
	interrupts <- os.Interrupt

	select {
	case forced := <-forcedClose:
		assert.True(t, forced)
	case <-time.After(5 * time.Second):
		t.Fatal("second interrupt did not force close")
	}
	select {
	case got := <-finished:
		require.NoError(t, got.err)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after second interrupt")
	}
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func TestRunCancellationKeepsInterruptedOutcomeWhenStdoutFinishFails(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	events := make(chan pisession.Event, 2)
	stateChecked := make(chan struct{})
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, events)
		session.state = bytes.Replace(session.state, []byte("false"), []byte("true"), 1)
		stateCalls := 0
		session.getState = func() (json.RawMessage, error) {
			stateCalls++
			if stateCalls == 2 {
				close(stateChecked)
			}
			return session.state, nil
		}
		return session, nil
	}
	stdoutErr := errors.New("stdout unavailable")
	stdout := newFinishErrorWriter(stdoutErr)
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(ctx, RunOptions{Type: "quick", Message: "wait", Stdout: stdout}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, stateChecked, "post-prompt state")
	events <- runEvent("agent_start", `{"type":"agent_start"}`)
	events <- runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial"}}`)
	waitRunSignal(t, stdout.observed, "assistant output")
	cancel()

	select {
	case got := <-finished:
		require.ErrorIs(t, got.err, stdoutErr)
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after cancellation")
	}
}

func TestRunBufferedInterruptWinsSelectedEventClosure(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	interrupts := make(chan os.Signal, 1)
	dependencies.interrupts = interrupts
	events := make(chan pisession.Event, 2)
	events <- runEvent("agent_start", `{"type":"agent_start"}`)
	events <- runEvent("message_update", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial"}}`)
	close(events)
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		return newScriptedRunSession(cfg, events), nil
	}
	stdout := newBlockingFinishWriter()
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(context.Background(), RunOptions{Type: "quick", Message: "wait", Stdout: stdout}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()

	waitRunSignal(t, stdout.finishing, "event-stream final newline")
	interrupts <- os.Interrupt
	close(stdout.release)

	select {
	case got := <-finished:
		require.ErrorContains(t, got.err, "event stream closed")
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after event stream closed")
	}
}

func TestRunAttemptsRegistryCleanupWhenCloseFails(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	prompted := make(chan struct{})
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		session := newScriptedRunSession(cfg, make(chan pisession.Event))
		session.prompted = prompted
		session.state = bytes.Replace(session.state, []byte("false"), []byte("true"), 1)
		session.close = func(context.Context) error { return errors.New("close failed") }
		return session, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		outcome RunOutcome
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		outcome, err := run(ctx, RunOptions{Type: "quick", Message: "wait"}, log.New(&bytes.Buffer{}), dependencies)
		finished <- result{outcome: outcome, err: err}
	}()
	waitRunSignal(t, prompted, "prompt")
	cancel()

	select {
	case got := <-finished:
		require.ErrorContains(t, got.err, "close failed")
		assert.Equal(t, RunInterrupted, got.outcome)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after cancellation")
	}
	registry := readRunRegistry(t, ws.Checkout)
	for _, record := range registry.Sessions {
		assert.Equal(t, store.StatusStopped, record.Status)
		assert.Zero(t, record.PID)
	}
}

func defaultRunTestDependencies() runDependencies {
	return runDependencies{
		getwd:        os.Getwd,
		resolvePiBin: func(string) (string, error) { return "pi", nil },
		checkPiVersion: func(context.Context, string) (pisession.VersionResult, error) {
			return pisession.VersionResult{Found: "0.82.1", Verified: true}, nil
		},
		spawn: func(ctx context.Context, cfg pisession.Config) (runSession, error) {
			return pisession.Spawn(ctx, cfg)
		},
		now: func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) },
	}
}

func runEvent(eventType, raw string) pisession.Event {
	return pisession.Event{Type: eventType, Raw: json.RawMessage(raw)}
}

type scriptedRunSession struct {
	pid         int
	state       json.RawMessage
	events      chan pisession.Event
	prompted    chan struct{}
	closed      chan bool
	onPrompt    func()
	prompt      func(context.Context, string, string) error
	startPrompt func(context.Context, string, string) (<-chan error, error)
	getState    func() (json.RawMessage, error)
	abort       func(context.Context) error
	close       func(context.Context) error
	closeOnce   sync.Once
}

func (s *scriptedRunSession) StartPrompt(ctx context.Context, message, image string) (<-chan error, error) {
	if s.startPrompt != nil {
		return s.startPrompt(ctx, message, image)
	}
	result := make(chan error, 1)
	go func() {
		defer close(result)
		result <- s.Prompt(ctx, message, image)
	}()
	return result, nil
}

func (s *scriptedRunSession) Prompt(ctx context.Context, message, image string) error {
	if s.prompted != nil {
		close(s.prompted)
	}
	if s.onPrompt != nil {
		s.onPrompt()
	}
	if s.prompt != nil {
		return s.prompt(ctx, message, image)
	}
	return nil
}

func (s *scriptedRunSession) Abort(ctx context.Context) error {
	if s.abort != nil {
		return s.abort(ctx)
	}
	return nil
}

func (s *scriptedRunSession) GetState(context.Context) (json.RawMessage, error) {
	if s.getState != nil {
		return s.getState()
	}
	return s.state, nil
}

func (s *scriptedRunSession) Events() <-chan pisession.Event {
	return s.events
}

func (s *scriptedRunSession) Close(ctx context.Context) error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.closed != nil {
			s.closed <- ctx.Err() == nil
		}
		if s.close != nil {
			closeErr = s.close(ctx)
		}
	})
	return closeErr
}

func (s *scriptedRunSession) ExitErr() error { return nil }
func (s *scriptedRunSession) PID() int       { return s.pid }

func newScriptedRunSession(cfg pisession.Config, events chan pisession.Event) *scriptedRunSession {
	return &scriptedRunSession{
		pid:    43100,
		events: events,
		state: json.RawMessage(fmt.Sprintf(
			`{"sessionId":%q,"sessionFile":%q,"isStreaming":false}`,
			cfg.SessionID,
			filepath.Join(cfg.SessionDir, "opaque.jsonl"),
		)),
	}
}

type observedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	observed chan struct{}
	once     sync.Once
}

func newObservedBuffer() *observedBuffer {
	return &observedBuffer{observed: make(chan struct{})}
}

func (w *observedBuffer) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written, err := w.buffer.Write(value)
	if written > 0 {
		w.once.Do(func() { close(w.observed) })
	}
	return written, err
}

func (w *observedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

type finishErrorWriter struct {
	buffer   bytes.Buffer
	err      error
	observed chan struct{}
	once     sync.Once
}

func newFinishErrorWriter(err error) *finishErrorWriter {
	return &finishErrorWriter{err: err, observed: make(chan struct{})}
}

func (w *finishErrorWriter) Write(value []byte) (int, error) {
	if bytes.Equal(value, []byte{'\n'}) {
		return 0, w.err
	}
	written, err := w.buffer.Write(value)
	if written > 0 {
		w.once.Do(func() { close(w.observed) })
	}
	return written, err
}

type blockingFinishWriter struct {
	buffer    bytes.Buffer
	finishing chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newBlockingFinishWriter() *blockingFinishWriter {
	return &blockingFinishWriter{finishing: make(chan struct{}), release: make(chan struct{})}
}

func (w *blockingFinishWriter) Write(value []byte) (int, error) {
	if bytes.Equal(value, []byte{'\n'}) {
		w.once.Do(func() { close(w.finishing) })
		<-w.release
	}
	return w.buffer.Write(value)
}

func waitRunSignal(t testing.TB, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitRunEventChannelFull(t testing.TB, events <-chan pisession.Event) {
	t.Helper()
	capacity := cap(events)
	if capacity == 0 {
		t.Fatal("pi event channel is unbuffered")
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		if len(events) == capacity {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for pi event channel saturation: len=%d cap=%d", len(events), capacity)
		}
	}
}

type observedDiagnosticWriter struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	match    []byte
	observed chan struct{}
	once     sync.Once
}

func newObservedDiagnosticWriter(match string) *observedDiagnosticWriter {
	return &observedDiagnosticWriter{match: []byte(match), observed: make(chan struct{})}
}

func (w *observedDiagnosticWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written, err := w.buffer.Write(value)
	if bytes.Contains(w.buffer.Bytes(), w.match) {
		w.once.Do(func() { close(w.observed) })
	}
	return written, err
}

func (w *observedDiagnosticWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

type blockingDiagnosticWriter struct {
	mu          sync.Mutex
	buffer      bytes.Buffer
	match       []byte
	blocked     chan struct{}
	released    chan struct{}
	blockOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingDiagnosticWriter(match string) *blockingDiagnosticWriter {
	return &blockingDiagnosticWriter{
		match:    []byte(match),
		blocked:  make(chan struct{}),
		released: make(chan struct{}),
	}
}

func (w *blockingDiagnosticWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	written, err := w.buffer.Write(value)
	shouldBlock := bytes.Contains(value, w.match)
	if shouldBlock {
		w.blockOnce.Do(func() { close(w.blocked) })
		<-w.released
	}
	w.mu.Unlock()
	return written, err
}

func (w *blockingDiagnosticWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func (w *blockingDiagnosticWriter) release() {
	w.releaseOnce.Do(func() { close(w.released) })
}

type runRegistryFile struct {
	Version  int                     `json:"version"`
	Sessions map[string]store.Record `json:"sessions"`
}

func assistantStopReasons(t testing.TB, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var reasons []string
	for _, line := range bytes.Split(bytes.TrimSpace(contents), []byte{'\n'}) {
		var record struct {
			Message struct {
				Role       string `json:"role"`
				StopReason string `json:"stopReason"`
			} `json:"message"`
		}
		require.NoError(t, json.Unmarshal(line, &record))
		if record.Message.Role == "assistant" {
			reasons = append(reasons, record.Message.StopReason)
		}
	}
	return reasons
}

func readRunRegistry(t testing.TB, checkout string) runRegistryFile {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(checkout, ".gibson", "state.json"))
	require.NoError(t, err)
	var registry runRegistryFile
	require.NoError(t, json.Unmarshal(contents, &registry))
	return registry
}
