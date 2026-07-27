package pitest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rpcRecord struct {
	ID                    string          `json:"id"`
	Type                  string          `json:"type"`
	Command               string          `json:"command"`
	Success               bool            `json:"success"`
	Data                  json.RawMessage `json:"data"`
	Messages              []rpcMessage    `json:"messages"`
	AssistantMessageEvent struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

type rpcMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func TestFakePiBasicSession(t *testing.T) {
	pi := BuildFakePi(t)

	output, err := exec.Command(pi, "--version").CombinedOutput()
	require.NoError(t, err, string(output))
	assert.Equal(t, "0.82.1\n", string(output))

	output, err = exec.Command(pi, "--mode", "rpc").CombinedOutput()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError, string(output))
	assert.Equal(t, 2, exitError.ExitCode())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cwd := t.TempDir()
	sessionDir := filepath.Join(cwd, "sessions")
	cmd := exec.CommandContext(ctx, pi,
		"--mode", "rpc",
		"--session-id", "s-20260727-abc123",
		"--session-dir", sessionDir,
	)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(stdout)
	require.NoError(t, encoder.Encode(map[string]any{"id": "ready", "type": "get_state"}))

	state := decodeRecord(t, decoder)
	assert.Equal(t, "ready", state.ID)
	assert.Equal(t, "response", state.Type)
	assert.Equal(t, "get_state", state.Command)
	assert.True(t, state.Success)

	var stateData struct {
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
		IsStreaming bool   `json:"isStreaming"`
	}
	require.NoError(t, json.Unmarshal(state.Data, &stateData))
	assert.Equal(t, "s-20260727-abc123", stateData.SessionID)
	assert.False(t, stateData.IsStreaming)
	require.NotEmpty(t, stateData.SessionFile)
	assert.Equal(t, sessionDir, filepath.Dir(stateData.SessionFile))

	require.NoError(t, encoder.Encode(map[string]any{
		"id":      "prompt",
		"type":    "prompt",
		"message": "Remember this prompt",
	}))
	accepted := decodeRecord(t, decoder)
	assert.Equal(t, "prompt", accepted.ID)
	assert.Equal(t, "response", accepted.Type)
	assert.Equal(t, "prompt", accepted.Command)
	assert.True(t, accepted.Success)

	var eventTypes []string
	var assistantText string
	var agentEndMessages []rpcMessage
	for {
		event := decodeRecord(t, decoder)
		eventTypes = append(eventTypes, event.Type)
		assert.NotEqual(t, "entry_appended", event.Type)
		if event.Type == "message_update" && event.AssistantMessageEvent.Type == "text_delta" {
			assistantText += event.AssistantMessageEvent.Delta
		}
		if event.Type == "agent_end" {
			agentEndMessages = event.Messages
		}
		if event.Type == "agent_settled" {
			break
		}
	}
	require.GreaterOrEqual(t, len(eventTypes), 7)
	assert.Equal(t, []string{"agent_start", "message_start"}, eventTypes[:2])
	for _, eventType := range eventTypes[2 : len(eventTypes)-3] {
		assert.Equal(t, "message_update", eventType)
	}
	assert.Equal(t, []string{"message_end", "agent_end", "agent_settled"}, eventTypes[len(eventTypes)-3:])
	assert.NotEmpty(t, assistantText)
	require.Len(t, agentEndMessages, 2)
	assert.Equal(t, "user", agentEndMessages[0].Role)
	assert.Equal(t, "Remember this prompt", agentEndMessages[0].Content)
	assert.Equal(t, "assistant", agentEndMessages[1].Role)

	require.NoError(t, stdin.Close())
	require.NoError(t, cmd.Wait(), stderr.String())

	assertSessionChain(t, stateData.SessionFile, cwd)
}

func TestFakePiExits143OnSIGTERM(t *testing.T) {
	pi := BuildFakePi(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cwd := t.TempDir()
	cmd := exec.CommandContext(ctx, pi,
		"--mode", "rpc",
		"--session-id", "s-20260727-term01",
		"--session-dir", filepath.Join(cwd, "sessions"),
	)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	require.NoError(t, json.NewEncoder(stdin).Encode(map[string]any{"id": "ready", "type": "get_state"}))
	ready := decodeRecord(t, json.NewDecoder(stdout))
	require.True(t, ready.Success)
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))

	err = cmd.Wait()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError, stderr.String())
	assert.Equal(t, 143, exitError.ExitCode())
}

func decodeRecord(t testing.TB, decoder *json.Decoder) rpcRecord {
	t.Helper()
	var record rpcRecord
	require.NoError(t, decoder.Decode(&record))
	return record
}

func assertSessionChain(t testing.TB, path, cwd string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	var records []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		records = append(records, append(json.RawMessage(nil), scanner.Bytes()...))
	}
	require.NoError(t, scanner.Err())
	require.Len(t, records, 3)

	var header struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
	}
	require.NoError(t, json.Unmarshal(records[0], &header))
	assert.Equal(t, "session", header.Type)
	assert.Equal(t, 3, header.Version)
	assert.Equal(t, "s-20260727-abc123", header.ID)
	assert.Equal(t, cwd, header.Cwd)
	assert.NotContains(t, filepath.Base(path), header.ID)

	var entries []struct {
		Type     string  `json:"type"`
		ID       string  `json:"id"`
		ParentID *string `json:"parentId"`
		Message  struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
	}
	for _, raw := range records[1:] {
		var entry struct {
			Type     string  `json:"type"`
			ID       string  `json:"id"`
			ParentID *string `json:"parentId"`
			Message  struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
		}
		require.NoError(t, json.Unmarshal(raw, &entry))
		entries = append(entries, entry)
	}

	entryID := regexp.MustCompile(`^[0-9a-f]{8}$`)
	assert.Equal(t, "message", entries[0].Type)
	assert.Regexp(t, entryID, entries[0].ID)
	assert.Nil(t, entries[0].ParentID)
	assert.Equal(t, "user", entries[0].Message.Role)
	assert.Equal(t, "Remember this prompt", entries[0].Message.Content)
	assert.Equal(t, "message", entries[1].Type)
	assert.Regexp(t, entryID, entries[1].ID)
	require.NotNil(t, entries[1].ParentID)
	assert.Equal(t, entries[0].ID, *entries[1].ParentID)
	assert.Equal(t, "assistant", entries[1].Message.Role)
}
