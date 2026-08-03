package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Status string

const (
	StatusLive    Status = "live"
	StatusStopped Status = "stopped"
	StatusClosed  Status = "closed"
)

type Record struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Status         Status `json:"status"`
	CreatedAt      string `json:"createdAt"`
	LastActivityAt string `json:"lastActivityAt"`
	PID            int    `json:"pid"`
}

type registry struct {
	Version  int               `json:"version"`
	Sessions map[string]Record `json:"sessions"`
}

func (s *Store) Put(record Record) error {
	record, err := normalizeRecord(record)
	if err != nil {
		return fmt.Errorf("put session: %w", err)
	}
	return s.withLockedState(func(state *registry) error {
		if _, exists := state.Sessions[record.ID]; exists {
			return fmt.Errorf("put session %q: already exists", record.ID)
		}
		state.Sessions[record.ID] = record
		return s.write(s.RegistryPath(), *state)
	})
}

func (s *Store) Touch(id string, at time.Time) error {
	return s.withLockedState(func(state *registry) error {
		record, ok := state.Sessions[id]
		if !ok {
			return fmt.Errorf("touch session %q: not found", id)
		}
		record.LastActivityAt = at.UTC().Format(time.RFC3339)
		state.Sessions[id] = record
		return s.write(s.RegistryPath(), *state)
	})
}

func (s *Store) SetLive(id string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("set session %q live: pid must be positive", id)
	}
	return s.withLockedState(func(state *registry) error {
		record, ok := state.Sessions[id]
		if !ok {
			return fmt.Errorf("set session %q live: not found", id)
		}
		if err := validateTransition(record.Status, StatusLive); err != nil {
			return fmt.Errorf("set session %q live: %w", id, err)
		}
		if record.Status == StatusLive && record.PID != pid {
			return fmt.Errorf("set session %q live: already owned by pid %d", id, record.PID)
		}
		record.Status = StatusLive
		record.PID = pid
		state.Sessions[id] = record
		return s.write(s.RegistryPath(), *state)
	})
}

func (s *Store) StopIfLivePID(id string, pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("stop session %q by pid: pid must be positive", id)
	}
	stopped := false
	err := s.withLockedState(func(state *registry) error {
		record, ok := state.Sessions[id]
		if !ok || record.Status != StatusLive || record.PID != pid {
			return nil
		}
		record.Status = StatusStopped
		record.PID = 0
		state.Sessions[id] = record
		if err := s.write(s.RegistryPath(), *state); err != nil {
			return err
		}
		stopped = true
		return nil
	})
	return stopped, err
}

func (s *Store) SetStatus(id string, status Status) error {
	if !status.valid() {
		return fmt.Errorf("set session %q status: invalid status %q", id, status)
	}
	return s.withLockedState(func(state *registry) error {
		record, ok := state.Sessions[id]
		if !ok {
			return fmt.Errorf("set session %q status: not found", id)
		}
		if err := validateTransition(record.Status, status); err != nil {
			return fmt.Errorf("set session %q status: %w", id, err)
		}
		if status == StatusLive && record.PID <= 0 {
			return fmt.Errorf("set session %q status: live status requires a positive pid", id)
		}
		record.Status = status
		if status == StatusStopped || status == StatusClosed {
			record.PID = 0
		}
		state.Sessions[id] = record
		return s.write(s.RegistryPath(), *state)
	})
}

func (s *Store) Get(id string) (Record, bool, error) {
	lock := checkoutLock(s.gibsonDir())
	if err := lock.lock(context.Background()); err != nil {
		return Record{}, false, err
	}
	defer lock.unlock()

	state, err := s.loadState()
	if err != nil {
		return Record{}, false, err
	}
	record, ok := state.Sessions[id]
	return record, ok, nil
}

func (s *Store) List() ([]Record, error) {
	lock := checkoutLock(s.gibsonDir())
	if err := lock.lock(context.Background()); err != nil {
		return nil, err
	}
	defer lock.unlock()

	state, err := s.loadState()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(state.Sessions))
	for id := range state.Sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	records := make([]Record, 0, len(ids))
	for _, id := range ids {
		records = append(records, state.Sessions[id])
	}
	return records, nil
}

func validateTransition(from, to Status) error {
	if !from.valid() || !to.valid() {
		return fmt.Errorf("invalid transition %q to %q", from, to)
	}
	if from == StatusClosed && to == StatusStopped {
		return fmt.Errorf("invalid transition %q to %q", from, to)
	}
	return nil
}

func (s Status) valid() bool {
	return s == StatusLive || s == StatusStopped || s == StatusClosed
}

func normalizeRecord(record Record) (Record, error) {
	if record.ID == "" {
		return Record{}, errors.New("id is required")
	}
	if !record.Status.valid() {
		return Record{}, fmt.Errorf("session %q has invalid status %q", record.ID, record.Status)
	}
	createdAt, err := normalizeTimestamp(record.CreatedAt)
	if err != nil {
		return Record{}, fmt.Errorf("session %q createdAt: %w", record.ID, err)
	}
	lastActivityAt, err := normalizeTimestamp(record.LastActivityAt)
	if err != nil {
		return Record{}, fmt.Errorf("session %q lastActivityAt: %w", record.ID, err)
	}
	record.CreatedAt = createdAt
	record.LastActivityAt = lastActivityAt
	if record.Status == StatusLive {
		if record.PID <= 0 {
			return Record{}, fmt.Errorf("session %q live status requires a positive pid", record.ID)
		}
	} else {
		record.PID = 0
	}
	return record, nil
}

func normalizeTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("must be RFC3339: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func (s *Store) withStoreLock(action func() error) error {
	return s.withStoreLockContext(context.Background(), action)
}

func (s *Store) withStoreLockContext(ctx context.Context, action func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.EnsureLayout(); err != nil {
		return err
	}
	process := checkoutLock(s.gibsonDir())
	if err := process.lock(ctx); err != nil {
		return fmt.Errorf("lock registry for checkout %s: %w", s.checkout, err)
	}
	defer process.unlock()

	lock, err := lockDirectory(ctx, s.gibsonDir())
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, lock.close())
	}()
	return action()
}

func (s *Store) withLockedState(update func(*registry) error) error {
	return s.withLockedStateContext(context.Background(), update)
}

func (s *Store) withLockedStateContext(ctx context.Context, update func(*registry) error) error {
	return s.withStoreLockContext(ctx, func() error {
		state, err := s.loadState()
		if err != nil {
			return err
		}
		return update(&state)
	})
}

func (s *Store) loadState() (registry, error) {
	contents, err := os.ReadFile(s.RegistryPath())
	if errors.Is(err, fs.ErrNotExist) {
		return newRegistry(), nil
	}
	if err != nil {
		return registry{}, fmt.Errorf("read registry %s: %w", s.RegistryPath(), err)
	}

	var state registry
	if err := json.Unmarshal(contents, &state); err != nil {
		return registry{}, fmt.Errorf("decode registry %s: %w", s.RegistryPath(), err)
	}
	if state.Version != 1 {
		return registry{}, fmt.Errorf("decode registry %s: unsupported version %d", s.RegistryPath(), state.Version)
	}
	if state.Sessions == nil {
		state.Sessions = make(map[string]Record)
	}
	for id, record := range state.Sessions {
		if record.ID != id {
			return registry{}, fmt.Errorf("decode registry %s: session key %q contains id %q", s.RegistryPath(), id, record.ID)
		}
		normalized, err := normalizeRecord(record)
		if err != nil {
			return registry{}, fmt.Errorf("decode registry %s: %w", s.RegistryPath(), err)
		}
		if normalized != record {
			return registry{}, fmt.Errorf("decode registry %s: session %q is not normalized", s.RegistryPath(), id)
		}
	}
	return state, nil
}

func newRegistry() registry {
	return registry{Version: 1, Sessions: make(map[string]Record)}
}

func writeRegistry(path string, state registry) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create registry temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set registry permissions: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync registry temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close registry temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace registry %s: %w", path, err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open registry directory for sync: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync registry directory: %w", err)
	}
	return nil
}
