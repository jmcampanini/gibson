package pisession

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
