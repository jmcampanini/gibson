package store

import (
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if record.ID == "" {
		return errors.New("put session: id is required")
	}
	if !record.Status.valid() {
		return fmt.Errorf("put session %q: invalid status %q", record.ID, record.Status)
	}
	createdAt, err := normalizeTimestamp(record.CreatedAt)
	if err != nil {
		return fmt.Errorf("put session %q: createdAt: %w", record.ID, err)
	}
	lastActivityAt, err := normalizeTimestamp(record.LastActivityAt)
	if err != nil {
		return fmt.Errorf("put session %q: lastActivityAt: %w", record.ID, err)
	}
	record.CreatedAt = createdAt
	record.LastActivityAt = lastActivityAt

	state, err := s.currentState()
	if err != nil {
		return err
	}
	state.Sessions[record.ID] = record
	return s.replaceState(state)
}

func (s *Store) Touch(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.currentState()
	if err != nil {
		return err
	}
	record, ok := state.Sessions[id]
	if !ok {
		return fmt.Errorf("touch session %q: not found", id)
	}
	record.LastActivityAt = at.UTC().Format(time.RFC3339)
	state.Sessions[id] = record
	return s.replaceState(state)
}

func (s *Store) SetStatus(id string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !status.valid() {
		return fmt.Errorf("set session %q status: invalid status %q", id, status)
	}
	state, err := s.currentState()
	if err != nil {
		return err
	}
	record, ok := state.Sessions[id]
	if !ok {
		return fmt.Errorf("set session %q status: not found", id)
	}
	record.Status = status
	if status == StatusStopped || status == StatusClosed {
		record.PID = 0
	}
	state.Sessions[id] = record
	return s.replaceState(state)
}

func (s *Store) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.currentState()
	if err != nil {
		return Record{}, false
	}
	record, ok := state.Sessions[id]
	return record, ok
}

func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.currentState()
	if err != nil {
		return nil
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
	return records
}

func (s Status) valid() bool {
	return s == StatusLive || s == StatusStopped || s == StatusClosed
}

func normalizeTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("must be RFC3339: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func (s *Store) currentState() (registry, error) {
	if !s.loaded {
		state, err := s.loadState()
		if err != nil {
			return registry{}, err
		}
		s.state = state
		s.loaded = true
	}
	return cloneRegistry(s.state), nil
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
	return state, nil
}

func (s *Store) replaceState(state registry) error {
	if err := writeRegistry(s.RegistryPath(), state); err != nil {
		return err
	}
	s.state = state
	s.loaded = true
	return nil
}

func newRegistry() registry {
	return registry{Version: 1, Sessions: make(map[string]Record)}
}

func cloneRegistry(source registry) registry {
	clone := registry{Version: source.Version, Sessions: make(map[string]Record, len(source.Sessions))}
	for id, record := range source.Sessions {
		clone.Sessions[id] = record
	}
	return clone
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
	return nil
}
