package store

import (
	"bytes"
	"context"
	"errors"
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

func TestCreateSessionAllocatesAndPersistsLiveIdentity(t *testing.T) {
	storage := newTestStore(t)
	var allocated string

	id, err := storage.CreateSession(context.Background(), func(id string) (SessionCreation, error) {
		allocated = id
		record := testRecord(1)
		record.ID = id
		return SessionCreation{Record: record, Rollback: func() error { return nil }}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, allocated, id)
	record, ok, err := storage.Get(id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, id, record.ID)
	assert.Equal(t, StatusLive, record.Status)
	assert.Positive(t, record.PID)
}

func TestCreateSessionRollsBackBeforeReconcilingFailedLiveWrite(t *testing.T) {
	storage := newTestStore(t)
	writeFailure := errors.New("live write failed")
	writeCalls := 0
	rolledBack := false
	storage.write = func(path string, state registry) error {
		writeCalls++
		if writeCalls == 1 {
			return writeFailure
		}
		if !rolledBack {
			return errors.New("stopped reconciliation ran before rollback")
		}
		return writeRegistry(path, state)
	}

	id, err := storage.CreateSession(context.Background(), func(id string) (SessionCreation, error) {
		record := testRecord(1)
		record.ID = id
		return SessionCreation{
			Record: record,
			Rollback: func() error {
				rolledBack = true
				return nil
			},
		}, nil
	})

	require.ErrorIs(t, err, writeFailure)
	assert.True(t, rolledBack)
	assert.Equal(t, 2, writeCalls)
	record, ok, getErr := storage.Get(id)
	require.NoError(t, getErr)
	require.True(t, ok)
	assert.Equal(t, StatusStopped, record.Status)
	assert.Zero(t, record.PID)
}

func TestCreateSessionRejectsNonLiveRecord(t *testing.T) {
	storage := newTestStore(t)

	rolledBack := false
	_, err := storage.CreateSession(context.Background(), func(id string) (SessionCreation, error) {
		record := testRecord(1)
		record.ID = id
		record.Status = StatusStopped
		return SessionCreation{
			Record: record,
			Rollback: func() error {
				rolledBack = true
				return nil
			},
		}, nil
	})

	require.ErrorContains(t, err, `expected "live"`)
	assert.True(t, rolledBack)
	records, listErr := storage.List()
	require.NoError(t, listErr)
	assert.Empty(t, records)
}

func TestSessionIDDateIsUTC(t *testing.T) {
	localTime := time.Date(2026, 7, 27, 1, 30, 0, 0, time.FixedZone("test", 2*60*60))
	id, err := newSessionID(localTime, bytes.NewReader([]byte{0, 1, 2, 3, 4, 5}))
	require.NoError(t, err)

	assert.Equal(t, "s-20260726-abcdef", id)
}
