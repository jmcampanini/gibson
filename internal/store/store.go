package store

import "path/filepath"

const gibsonDirName = ".gibson"

type Store struct {
	checkout string
	write    func(string, registry) error
}

func Open(checkoutPath string) *Store {
	checkout := filepath.Clean(checkoutPath)
	if absolute, err := filepath.Abs(checkout); err == nil {
		checkout = absolute
	}
	return &Store{checkout: checkout, write: writeRegistry}
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

func (s *Store) RegistryPath() string {
	return filepath.Join(s.checkout, gibsonDirName, "state.json")
}

func (s *Store) StderrLogPath(id string) string {
	return filepath.Join(s.logsDir(), id+".stderr.log")
}

func (s *Store) gibsonDir() string {
	return filepath.Join(s.checkout, gibsonDirName)
}

func (s *Store) logsDir() string {
	return filepath.Join(s.gibsonDir(), "logs")
}
