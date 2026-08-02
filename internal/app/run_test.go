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

func TestRunPresentsDialogWarningWhilePromptAcceptanceIsPending(t *testing.T) {
	ws := testws.New(t, testws.WithSessionType("quick", config.SessionType{Description: "Quick"}))
	dependencies := defaultRunTestDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	releasePrompt := make(chan struct{})
	stderr := &dialogReleaseWriter{release: releasePrompt}
	dependencies.spawn = func(_ context.Context, cfg pisession.Config) (runSession, error) {
		events := make(chan pisession.Event, 1)
		session := newScriptedRunSession(cfg, events)
		session.prompt = func(ctx context.Context, _, _ string) error {
			events <- runEvent("extension_ui_request", `{"type":"extension_ui_request","method":"confirm","message":"Continue?"}`)
			select {
			case <-releasePrompt:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return session, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outcome, err := run(ctx, RunOptions{Type: "quick", Message: "/dialog", Stderr: stderr}, log.New(&bytes.Buffer{}), dependencies)

	require.NoError(t, err)
	assert.Equal(t, RunCompleted, outcome)
	assert.Contains(t, stderr.String(), "[warning] pi is waiting for a confirm dialog; gibson run cannot answer dialogs")
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
	pid       int
	state     json.RawMessage
	events    chan pisession.Event
	prompted  chan struct{}
	closed    chan bool
	onPrompt  func()
	prompt    func(context.Context, string, string) error
	getState  func() (json.RawMessage, error)
	closeOnce sync.Once
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
	s.closeOnce.Do(func() {
		if s.closed != nil {
			s.closed <- ctx.Err() == nil
		}
	})
	return nil
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

type dialogReleaseWriter struct {
	bytes.Buffer
	release chan struct{}
	once    sync.Once
}

func (w *dialogReleaseWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(w.Bytes(), []byte("cannot answer dialogs")) {
		w.once.Do(func() { close(w.release) })
	}
	return n, err
}

type runRegistryFile struct {
	Version  int                     `json:"version"`
	Sessions map[string]store.Record `json:"sessions"`
}

func readRunRegistry(t testing.TB, checkout string) runRegistryFile {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(checkout, ".gibson", "state.json"))
	require.NoError(t, err)
	var registry runRegistryFile
	require.NoError(t, json.Unmarshal(contents, &registry))
	return registry
}
