package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestCreateSessionToleratesEmptySessionFile(t *testing.T) {
	storage := newTestStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(storage.SessionsDir(), "truncated.jsonl"), nil, 0o600))

	id, err := storage.CreateSession(context.Background(), func(id string) (SessionCreation, error) {
		record := testRecord(1)
		record.ID = id
		return SessionCreation{Record: record, Rollback: func() error { return nil }}, nil
	})

	require.NoError(t, err)
	_, ok, getErr := storage.Get(id)
	require.NoError(t, getErr)
	assert.True(t, ok)
}

func TestCreateSessionRejectsMalformedNonEmptySessionFile(t *testing.T) {
	storage := newTestStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(storage.SessionsDir(), "broken.jsonl"), []byte("{"), 0o600))

	created := false
	_, err := storage.CreateSession(context.Background(), func(id string) (SessionCreation, error) {
		created = true
		return SessionCreation{}, nil
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "session header")
	assert.False(t, created)
}
