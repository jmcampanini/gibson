package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type sessionHeader struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	ID      string `json:"id"`
}

func (s *Store) FindSessionFile(id string) (path string, err error) {
	if id == "" {
		return "", errors.New("find session file: id is required")
	}
	err = s.withStoreLock(func() error {
		headers, err := s.loadSessionHeaders()
		if err != nil {
			return err
		}
		found, ok := headers[id]
		if !ok {
			return fmt.Errorf("find session file %q: not found", id)
		}
		path = found
		return nil
	})
	return path, err
}

func (s *Store) loadSessionHeaders() (map[string]string, error) {
	entries, err := os.ReadDir(s.SessionsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session directory %s: %w", s.SessionsDir(), err)
	}
	headers := make(map[string]string)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(s.SessionsDir(), entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("read session header %s: symbolic links are not allowed", path)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect session file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("read session header %s: not a regular file", path)
		}
		header, err := readSessionHeader(path)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := headers[header.ID]; duplicate {
			return nil, fmt.Errorf("duplicate session id %q in %s and %s", header.ID, previous, path)
		}
		headers[header.ID] = path
	}
	return headers, nil
}

func readSessionHeader(path string) (sessionHeader, error) {
	file, err := os.Open(path)
	if err != nil {
		return sessionHeader{}, fmt.Errorf("open session header %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	line, readErr := bufio.NewReader(file).ReadBytes('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return sessionHeader{}, fmt.Errorf("read session header %s: %w", path, readErr)
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 {
		return sessionHeader{}, fmt.Errorf("read session header %s: empty header", path)
	}
	var header sessionHeader
	if err := json.Unmarshal(line, &header); err != nil {
		return sessionHeader{}, fmt.Errorf("decode session header %s: %w", path, err)
	}
	if header.Type != "session" || header.Version != 3 || header.ID == "" {
		return sessionHeader{}, fmt.Errorf("decode session header %s: expected type session, version 3, and a nonempty id", path)
	}
	return header, nil
}
