package store

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvailableSessionIDRegeneratesAfterRegistryAndHeaderCollisions(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	state := newRegistry()
	state.Sessions["s-20260726-abcdef"] = Record{ID: "s-20260726-abcdef"}
	headers := map[string]string{"s-20260726-ghijkl": "opaque.jsonl"}
	random := bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17})

	id, err := availableSessionID(&state, headers, now, random)

	require.NoError(t, err)
	assert.Equal(t, "s-20260726-mnopqr", id)
}

func TestSessionIDDateIsUTC(t *testing.T) {
	localTime := time.Date(2026, 7, 27, 1, 30, 0, 0, time.FixedZone("test", 2*60*60))
	id, err := newSessionID(localTime, bytes.NewReader([]byte{0, 1, 2, 3, 4, 5}))
	require.NoError(t, err)

	assert.Equal(t, "s-20260726-abcdef", id)
}
