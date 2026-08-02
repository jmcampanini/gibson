package main

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/jmcampanini/gibson/internal/fakepi/scenarios"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptAcceptanceImmediatelyReportsStreaming(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	finished := make(chan error, 1)
	sessionDir := t.TempDir()
	go func() {
		finished <- run(config{
			sessionID:  "s-20260802-streaming",
			sessionDir: sessionDir,
		}, scenarios.Scenario{
			Name: "delayed_start",
			Steps: []scenarios.Step{
				{Type: scenarios.AgentStart, Delay: time.Hour},
				{Type: scenarios.AgentSettled},
			},
		}, inputReader, outputWriter)
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		_ = inputReader.Close()
		_ = outputWriter.Close()
		_ = outputReader.Close()
	})

	encoder := json.NewEncoder(inputWriter)
	decoder := json.NewDecoder(outputReader)
	require.NoError(t, encoder.Encode(map[string]any{
		"id":      "prompt",
		"type":    "prompt",
		"message": "start later",
	}))
	accepted := decodeFakePiRecord(t, decoder)
	assert.Equal(t, "prompt", accepted.ID)
	assert.True(t, accepted.Success)

	require.NoError(t, encoder.Encode(map[string]any{"id": "state", "type": "get_state"}))
	state := decodeFakePiRecord(t, decoder)
	assert.Equal(t, "state", state.ID)
	var stateData struct {
		IsStreaming bool `json:"isStreaming"`
	}
	require.NoError(t, json.Unmarshal(state.Data, &stateData))
	assert.True(t, stateData.IsStreaming)

	require.NoError(t, encoder.Encode(map[string]any{"id": "abort", "type": "abort"}))
	for {
		record := decodeFakePiRecord(t, decoder)
		if record.Type == "response" && record.ID == "abort" {
			assert.True(t, record.Success)
			break
		}
	}
	require.NoError(t, inputWriter.Close())
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("fake pi did not exit after aborted prompt")
	}
}

type fakePiTestRecord struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

func decodeFakePiRecord(t testing.TB, decoder *json.Decoder) fakePiTestRecord {
	t.Helper()
	var record fakePiTestRecord
	require.NoError(t, decoder.Decode(&record))
	return record
}
