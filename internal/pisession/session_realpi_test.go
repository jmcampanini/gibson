package pisession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestRealPiPromptStateOrdering(t *testing.T) {
	piBin := pitest.RequireRealPi(t)
	version, err := CheckPiVersion(context.Background(), piBin)
	require.NoError(t, err)
	ws := testws.New(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(ws.Root, "pi-agent"))
	t.Setenv("PI_OFFLINE", "1")
	releasePath := filepath.Join(ws.Root, "release-provider")
	extensionPath := writePromptOrderingExtension(t, ws.Root, releasePath)
	sessionID := "s-20260802-ordering"
	sessionDir := filepath.Join(ws.Checkout, ".gibson", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := Spawn(ctx, Config{
		PiBin:      piBin,
		SessionID:  sessionID,
		SessionDir: sessionDir,
		Cwd:        ws.Checkout,
		Model:      "gibson-test/deterministic",
		ExtraArgs:  []string{"--extension", extensionPath},
		StderrPath: filepath.Join(ws.Checkout, ".gibson", "logs", sessionID+".stderr.log"),
	})
	require.NoError(t, err)
	cleanupSession(t, session)

	require.NoError(t, session.Prompt(ctx, "start a real agent run", ""))
	stateRaw, err := session.GetState(ctx)
	require.NoError(t, err)
	var state struct {
		IsStreaming bool `json:"isStreaming"`
	}
	require.NoError(t, json.Unmarshal(stateRaw, &state))
	assert.True(t, state.IsStreaming, "pi %s did not expose the accepted agent run before processing get_state", version.Found)
	require.NoError(t, os.WriteFile(releasePath, []byte("release\n"), 0o600))

	sawAgentStart := false
	for {
		select {
		case event, open := <-session.Events():
			require.True(t, open, "real pi exited before agent_settled")
			sawAgentStart = sawAgentStart || event.Type == "agent_start"
			if event.Type == "agent_settled" {
				assert.True(t, sawAgentStart)
				goto settled
			}
		case <-ctx.Done():
			t.Fatal("waiting for real pi agent_settled:", ctx.Err())
		}
	}

settled:
	require.NoError(t, session.Prompt(ctx, "/handled", ""))
	stateRaw, err = session.GetState(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(stateRaw, &state))
	assert.False(t, state.IsStreaming, "pi %s reported an agent run for a handled extension command", version.Found)
}

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

	// Tripwire for the exact-string cursor classification: real pi, not fakepi,
	// must produce the wording GetEntries maps to ErrInvalidCursor.
	_, _, badCursorErr := session.GetEntries(ctx, "bogus-cursor")
	require.ErrorIs(t, badCursorErr, ErrInvalidCursor)

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

func writePromptOrderingExtension(t testing.TB, directory, releasePath string) string {
	t.Helper()
	releaseJSON, err := json.Marshal(releasePath)
	require.NoError(t, err)
	source := fmt.Sprintf(`
import { existsSync } from "node:fs";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

const releasePath = %s;

export default function (pi) {
  pi.registerCommand("handled", {
    description: "Handle without starting an agent",
    handler: async () => {},
  });

  pi.registerProvider("gibson-test", {
    baseUrl: "http://127.0.0.1",
    apiKey: "test",
    api: "gibson-test-api",
    models: [{
      id: "deterministic",
      name: "Gibson deterministic test",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 4096,
      maxTokens: 32,
    }],
    streamSimple(model, _context, options) {
      const stream = createAssistantMessageEventStream();
      const output = {
        role: "assistant",
        content: [],
        api: model.api,
        provider: model.provider,
        model: model.id,
        usage: {
          input: 0,
          output: 0,
          cacheRead: 0,
          cacheWrite: 0,
          totalTokens: 0,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
        },
        stopReason: "pending",
        timestamp: Date.now(),
      };

      (async () => {
        stream.push({ type: "start", partial: output });
        while (!existsSync(releasePath)) {
          if (options?.signal?.aborted) throw new Error("aborted");
          await new Promise((resolve) => setTimeout(resolve, 5));
        }
        const text = { type: "text", text: "deterministic response" };
        output.content.push(text);
        stream.push({ type: "text_start", contentIndex: 0, partial: output });
        stream.push({ type: "text_delta", contentIndex: 0, delta: text.text, partial: output });
        stream.push({ type: "text_end", contentIndex: 0, content: text.text, partial: output });
        output.stopReason = "stop";
        output.usage.output = 2;
        output.usage.totalTokens = 2;
        stream.push({ type: "done", reason: "stop", message: output });
        stream.end();
      })().catch((error) => {
        output.stopReason = options?.signal?.aborted ? "aborted" : "error";
        output.errorMessage = String(error);
        stream.push({ type: "error", reason: output.stopReason, error: output });
        stream.end();
      });

      return stream;
    },
  });
}
`, releaseJSON)
	path := filepath.Join(directory, "prompt-ordering-extension.ts")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return path
}
