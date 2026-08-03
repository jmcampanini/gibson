package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	require.NoError(t, reopened.SetLive(record.ID, record.PID))
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

func TestRegistryEnforcesLifecycleTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    Status
		to      Status
		wantErr bool
	}{
		{name: "live remains live", from: StatusLive, to: StatusLive},
		{name: "live stops", from: StatusLive, to: StatusStopped},
		{name: "live closes", from: StatusLive, to: StatusClosed},
		{name: "stopped remains stopped", from: StatusStopped, to: StatusStopped},
		{name: "stopped resumes", from: StatusStopped, to: StatusLive},
		{name: "stopped closes", from: StatusStopped, to: StatusClosed},
		{name: "closed remains closed", from: StatusClosed, to: StatusClosed},
		{name: "closed reopens", from: StatusClosed, to: StatusLive},
		{name: "closed cannot become stopped", from: StatusClosed, to: StatusStopped, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := newTestStore(t)
			record := testRecord(1)
			record.Status = test.from
			if test.from != StatusLive {
				record.PID = 0
			}
			require.NoError(t, storage.Put(record))

			var err error
			if test.to == StatusLive && test.from != StatusLive {
				err = storage.SetLive(record.ID, 9001)
			} else {
				err = storage.SetStatus(record.ID, test.to)
			}
			if test.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, `invalid transition "closed" to "stopped"`)
				return
			}
			require.NoError(t, err)
			got, ok := storage.Get(record.ID)
			require.True(t, ok)
			assert.Equal(t, test.to, got.Status)
			assert.Equal(t, record.Name, got.Name)
			assert.Equal(t, record.Type, got.Type)
			assert.Equal(t, record.CreatedAt, got.CreatedAt)
			assert.Equal(t, record.LastActivityAt, got.LastActivityAt)
			if test.to == StatusLive {
				assert.Positive(t, got.PID)
			} else {
				assert.Zero(t, got.PID)
			}
		})
	}
}

func TestRegistryEnforcesPIDAndIdentityInvariants(t *testing.T) {
	storage := newTestStore(t)
	invalidLive := testRecord(1)
	invalidLive.PID = 0
	require.ErrorContains(t, storage.Put(invalidLive), "live status requires a positive pid")

	stopped := testRecord(2)
	stopped.Status = StatusStopped
	require.NoError(t, storage.Put(stopped))
	got, ok := storage.Get(stopped.ID)
	require.True(t, ok)
	assert.Zero(t, got.PID)
	require.ErrorContains(t, storage.SetLive(stopped.ID, 0), "pid must be positive")

	live := testRecord(3)
	require.NoError(t, storage.Put(live))
	require.ErrorContains(t, storage.SetLive(live.ID, live.PID+1), "already owned")
	require.ErrorContains(t, storage.Put(live), "already exists")
}

func TestStopIfLivePIDCannotStopAnotherOwner(t *testing.T) {
	storage := newTestStore(t)
	record := testRecord(1)
	require.NoError(t, storage.Put(record))

	stopped, err := storage.StopIfLivePID(record.ID, record.PID+1)
	require.NoError(t, err)
	assert.False(t, stopped)
	got, ok := storage.Get(record.ID)
	require.True(t, ok)
	assert.Equal(t, StatusLive, got.Status)
	assert.Equal(t, record.PID, got.PID)

	stopped, err = storage.StopIfLivePID(record.ID, record.PID)
	require.NoError(t, err)
	assert.True(t, stopped)
	got, ok = storage.Get(record.ID)
	require.True(t, ok)
	assert.Equal(t, StatusStopped, got.Status)
	assert.Zero(t, got.PID)
}

func TestRegistryIgnoresLeftoverTemporaryFilesAndNormalizesMode(t *testing.T) {
	storage := newTestStore(t)
	require.NoError(t, storage.Put(testRecord(1)))
	require.NoError(t, os.Chmod(storage.RegistryPath(), 0o600))
	leftover := filepath.Join(storage.gibsonDir(), ".state-leftover.tmp")
	require.NoError(t, os.WriteFile(leftover, []byte(`{"incomplete":`), 0o600))

	require.NoError(t, storage.Put(testRecord(2)))

	assert.FileExists(t, leftover)
	info, err := os.Stat(storage.RegistryPath())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assert.Len(t, storage.List(), 2)
}

func TestRegistryLocksAcrossProcessesAndReloadsBeforeMutation(t *testing.T) {
	storage := newTestStore(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	parentDone := make(chan error, 1)
	go func() {
		_, err := storage.CreateSession(func(id string) (SessionCreation, error) {
			close(entered)
			<-release
			record := testRecord(1)
			record.ID = id
			return SessionCreation{Record: record, Rollback: func() error { return nil }}, nil
		})
		parentDone <- err
	}()
	<-entered

	child, donePath := startRegistryMutationHelper(t, storage)
	releaseLock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseLock)
	time.Sleep(100 * time.Millisecond)
	_, err := os.Stat(donePath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	releaseLock()
	require.NoError(t, <-parentDone)
	require.NoError(t, child.Wait())
	assert.FileExists(t, donePath)
	assert.Len(t, storage.List(), 2)
}

func TestCreateSessionWriteFailureKeepsProcessLockThroughRollback(t *testing.T) {
	storage := newTestStore(t)
	writeFailure := errors.New("live write failed")
	writeCalls := 0
	storage.write = func(path string, state registry) error {
		writeCalls++
		if writeCalls == 1 {
			return writeFailure
		}
		return writeRegistry(path, state)
	}
	rollbackStarted := make(chan struct{})
	releaseRollback := make(chan struct{})
	createDone := make(chan error, 1)
	go func() {
		_, err := storage.CreateSession(func(id string) (SessionCreation, error) {
			record := testRecord(1)
			record.ID = id
			return SessionCreation{
				Record: record,
				Rollback: func() error {
					close(rollbackStarted)
					<-releaseRollback
					return nil
				},
			}, nil
		})
		createDone <- err
	}()
	<-rollbackStarted

	child, donePath := startRegistryMutationHelper(t, storage)
	release := sync.OnceFunc(func() { close(releaseRollback) })
	t.Cleanup(release)
	time.Sleep(100 * time.Millisecond)
	_, err := os.Stat(donePath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	release()
	require.ErrorIs(t, <-createDone, writeFailure)
	require.NoError(t, child.Wait())
	assert.FileExists(t, donePath)
	records := storage.List()
	require.Len(t, records, 2)
	assert.Contains(t, []Status{records[0].Status, records[1].Status}, StatusStopped)
}

func startRegistryMutationHelper(t *testing.T, storage *Store) (*exec.Cmd, string) {
	t.Helper()
	stateDir := t.TempDir()
	startedPath := filepath.Join(stateDir, "started")
	donePath := filepath.Join(stateDir, "done")
	child := exec.Command(os.Args[0], "-test.run", "^TestRegistryMutationHelper$")
	child.Env = append(os.Environ(),
		"GIBSON_TEST_REGISTRY_HELPER=1",
		"GIBSON_TEST_REGISTRY_CHECKOUT="+storage.checkout,
		"GIBSON_TEST_REGISTRY_STARTED="+startedPath,
		"GIBSON_TEST_REGISTRY_DONE="+donePath,
	)
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(startedPath)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return child, donePath
}

func TestRegistryMutationHelper(t *testing.T) {
	if os.Getenv("GIBSON_TEST_REGISTRY_HELPER") != "1" {
		return
	}
	require.NoError(t, os.WriteFile(os.Getenv("GIBSON_TEST_REGISTRY_STARTED"), []byte("started"), 0o600))
	storage := Open(os.Getenv("GIBSON_TEST_REGISTRY_CHECKOUT"))
	record := testRecord(2)
	require.NoError(t, storage.Put(record))
	require.NoError(t, os.WriteFile(os.Getenv("GIBSON_TEST_REGISTRY_DONE"), []byte("done"), 0o600))
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
