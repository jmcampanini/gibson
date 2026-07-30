package pisession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jmcampanini/gibson/internal/pitest"
	"github.com/jmcampanini/gibson/internal/testws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealPiNoPromptLifecycle(t *testing.T) {
	piBin := pitest.RequireRealPi(t)
	version, err := CheckPiVersion(context.Background(), piBin)
	require.NoError(t, err)
	ws := testws.New(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(ws.Root, "pi-agent"))
	t.Setenv("PI_OFFLINE", "1")
	sessionID := "s-20260730-realpi"
	sessionDir := filepath.Join(ws.Checkout, ".gibson", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	cfg := Config{
		PiBin:      piBin,
		SessionID:  sessionID,
		SessionDir: sessionDir,
		Cwd:        ws.Checkout,
		StderrPath: filepath.Join(ws.Checkout, ".gibson", "logs", sessionID+".stderr.log"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session, err := Spawn(ctx, cfg)
	require.NoError(t, err)
	cleanupSession(t, session)

	stateRaw, err := session.GetState(ctx)
	require.NoError(t, err)
	var state struct {
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
		IsStreaming bool   `json:"isStreaming"`
	}
	require.NoError(t, json.Unmarshal(stateRaw, &state))
	assert.Equal(t, sessionID, state.SessionID)
	require.NotEmpty(t, state.SessionFile)
	assert.Equal(t, sessionDir, filepath.Dir(state.SessionFile))
	assert.False(t, state.IsStreaming)

	entriesBeforeName, leafBeforeName, err := session.GetEntries(ctx, "")
	require.NoError(t, err)

	const sessionName = "Gibson real pi lifecycle"
	require.NoError(t, session.SetSessionName(ctx, sessionName))
	for {
		select {
		case event, ok := <-session.Events():
			require.True(t, ok, "real pi exited before session_info_changed")
			if event.Type != "session_info_changed" {
				continue
			}
			var changed struct {
				Name string `json:"name"`
			}
			require.NoError(t, json.Unmarshal(event.Raw, &changed))
			assert.Equal(t, sessionName, changed.Name)
		case <-ctx.Done():
			t.Fatal("waiting for real pi session_info_changed:", ctx.Err())
		}
		break
	}

	entriesAfterName, leafID, err := session.GetEntries(ctx, "")
	require.NoError(t, err)
	require.Len(t, entriesAfterName, len(entriesBeforeName)+1)
	require.NotEmpty(t, leafID)
	newEntries := entriesAfterName
	if leafBeforeName != "" {
		newEntries, _, err = session.GetEntries(ctx, leafBeforeName)
		require.NoError(t, err)
	}
	require.Len(t, newEntries, 1)
	var sessionInfo struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(newEntries[0], &sessionInfo))
	assert.Equal(t, "session_info", sessionInfo.Type)
	assert.Equal(t, leafID, sessionInfo.ID)
	assert.Equal(t, sessionName, sessionInfo.Name)

	require.NoError(t, session.Close(ctx))
	select {
	case <-session.Done():
	default:
		t.Fatal("Close returned before real pi was reaped")
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, session.ExitErr(), &exitErr)
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok, "real pi exit status is not syscall.WaitStatus")
	assert.Equal(t, 143, status.ExitStatus())
	_, open := <-session.Events()
	assert.False(t, open)

	contents, err := os.ReadFile(state.SessionFile)
	if errors.Is(err, os.ErrNotExist) && !version.Verified {
		t.Logf("unverified pi %s defers creating a session file until the first assistant message", version.Found)
		return
	}
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(contents), []byte{'\n'})
	require.Len(t, lines, len(entriesAfterName)+1)
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Cwd  string `json:"cwd"`
	}
	require.NoError(t, json.Unmarshal(lines[0], &header))
	assert.Equal(t, "session", header.Type)
	assert.Equal(t, sessionID, header.ID)
	assert.Equal(t, ws.Checkout, header.Cwd)
}
