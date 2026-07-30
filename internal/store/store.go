package store

import (
	"path/filepath"
	"sync"
)

const gibsonDirName = ".gibson"

type Store struct {
	checkout string
	state    registry
	loaded   bool
	mu       sync.Mutex
}

func Open(checkoutPath string) *Store {
	return &Store{checkout: filepath.Clean(checkoutPath)}
}

func (s *Store) EnsureLayout() error {
	if err := makeDir(s.SessionsDir()); err != nil {
		return err
	}
	return makeDir(s.logsDir())
}

func (s *Store) SessionsDir() string {
	return filepath.Join(s.checkout, gibsonDirName, "sessions")
}

func (s *Store) RegistryPath() string {
	return filepath.Join(s.checkout, gibsonDirName, "state.json")
}

func (s *Store) StderrLogPath(id string) string {
	return filepath.Join(s.logsDir(), id+".stderr.log")
}

func (s *Store) logsDir() string {
	return filepath.Join(s.checkout, gibsonDirName, "logs")
}
