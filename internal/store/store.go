package store

import (
	"fmt"
	"path/filepath"
)

const gibsonDirName = ".gibson"

type Store struct {
	checkout string
	write    func(string, registry) error
}

func Open(checkoutPath string) (*Store, error) {
	checkout, err := filepath.Abs(checkoutPath)
	if err != nil {
		return nil, fmt.Errorf("resolve checkout path %s: %w", checkoutPath, err)
	}
	return &Store{checkout: checkout, write: writeRegistry}, nil
}

func (s *Store) EnsureLayout() error {
	for _, path := range []string{s.gibsonDir(), s.SessionsDir(), s.logsDir()} {
		if err := makeDir(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SessionsDir() string {
	return filepath.Join(s.checkout, gibsonDirName, "sessions")
}

func (s *Store) StderrLogPath(id string) string {
	return filepath.Join(s.logsDir(), id+".stderr.log")
}

func (s *Store) registryPath() string {
	return filepath.Join(s.checkout, gibsonDirName, "state.json")
}

func (s *Store) gibsonDir() string {
	return filepath.Join(s.checkout, gibsonDirName)
}

func (s *Store) logsDir() string {
	return filepath.Join(s.gibsonDir(), "logs")
}
