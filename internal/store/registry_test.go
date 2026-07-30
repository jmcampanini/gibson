package store

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryPersistsVersionedSessionLifecycle(t *testing.T) {
	storage := newTestStore(t)
	record := Record{
		ID:             "s-20260726-abc123",
		Name:           "Refactor auth",
		Type:           "review",
		Status:         StatusLive,
		CreatedAt:      "2026-07-26T16:00:00+02:00",
		LastActivityAt: "2026-07-26T16:05:00+02:00",
		PID:            12345,
	}
	require.NoError(t, storage.Put(record))

	touchedAt := time.Date(2026, 7, 26, 18, 6, 7, 0, time.FixedZone("west", -4*60*60))
	require.NoError(t, storage.Touch(record.ID, touchedAt))
	require.NoError(t, storage.SetStatus(record.ID, StatusStopped))

	reopened := Open(storage.checkout)
	got, ok := reopened.Get(record.ID)
	require.True(t, ok)
	want := record
	want.CreatedAt = "2026-07-26T14:00:00Z"
	want.LastActivityAt = "2026-07-26T22:06:07Z"
	want.Status = StatusStopped
	want.PID = 0
	assert.Equal(t, want, got)

	assertRegistryShape(t, storage.RegistryPath(), record.ID)

	require.NoError(t, reopened.Put(record))
	require.NoError(t, reopened.SetStatus(record.ID, StatusClosed))
	closed, ok := reopened.Get(record.ID)
	require.True(t, ok)
	assert.Equal(t, StatusClosed, closed.Status)
	assert.Zero(t, closed.PID)
}

func TestRegistrySerializesConcurrentMutationsWithoutPartialState(t *testing.T) {
	storage := newTestStore(t)
	initial := testRecord(0)
	require.NoError(t, storage.Put(initial))

	const writers = 40
	start := make(chan struct{})
	readerDone := make(chan struct{})
	readerErr := make(chan error, 1)
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-start:
				return
			default:
			}
			contents, err := os.ReadFile(storage.RegistryPath())
			if err != nil {
				readerErr <- err
				return
			}
			var state registry
			if err := json.Unmarshal(contents, &state); err != nil {
				readerErr <- fmt.Errorf("observed partial registry: %w", err)
				return
			}
		}
	}()

	var wait sync.WaitGroup
	errors := make(chan error, writers)
	for i := 1; i <= writers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errors <- storage.Put(testRecord(index))
		}(i)
	}
	wait.Wait()
	close(errors)
	close(start)
	<-readerDone

	select {
	case err := <-readerErr:
		require.NoError(t, err)
	default:
	}
	for err := range errors {
		require.NoError(t, err)
	}

	records := Open(storage.checkout).List()
	require.Len(t, records, writers+1)
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.ID
	}
	wantIDs := slices.Clone(ids)
	slices.Sort(wantIDs)
	assert.Equal(t, wantIDs, ids)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	storage := Open(t.TempDir())
	require.NoError(t, storage.EnsureLayout())
	return storage
}

func testRecord(index int) Record {
	return Record{
		ID:             fmt.Sprintf("s-20260726-%06d", index),
		Name:           fmt.Sprintf("Session %d", index),
		Type:           "quick",
		Status:         StatusLive,
		CreatedAt:      "2026-07-26T14:00:00Z",
		LastActivityAt: "2026-07-26T14:00:00Z",
		PID:            1000 + index,
	}
}

func assertRegistryShape(t *testing.T, path, id string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(contents, &document))
	assert.Equal(t, []string{"sessions", "version"}, sortedKeys(document))

	var version int
	require.NoError(t, json.Unmarshal(document["version"], &version))
	assert.Equal(t, 1, version)

	var sessions map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["sessions"], &sessions))
	assert.Equal(
		t,
		[]string{"createdAt", "id", "lastActivityAt", "name", "pid", "status", "type"},
		sortedKeys(sessions[id]),
	)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
