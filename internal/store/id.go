package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"
)

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

type SessionCreation struct {
	Record   Record
	Rollback func() error
}

func (s *Store) CreateSession(ctx context.Context, create func(string) (SessionCreation, error)) (id string, err error) {
	if ctx == nil {
		return "", errors.New("create session: context is required")
	}
	if create == nil {
		return "", errors.New("create session: callback is required")
	}
	err = s.withLockedStateContext(ctx, func(state *registry) error {
		headers, err := s.loadSessionHeaders()
		if err != nil {
			return err
		}
		id, err = availableSessionID(state, headers, time.Now().UTC(), rand.Reader)
		if err != nil {
			return err
		}
		creation, err := create(id)
		if err != nil {
			return err
		}
		if creation.Rollback == nil {
			return errors.New("create session: rollback callback is required")
		}
		rollback := func(cause error) error {
			return errors.Join(cause, creation.Rollback())
		}
		record := creation.Record
		if record.ID == "" {
			record.ID = id
		}
		if record.ID != id {
			return rollback(fmt.Errorf("create session %q: callback returned record id %q", id, record.ID))
		}
		record, err = normalizeRecord(record)
		if err != nil {
			return rollback(err)
		}
		if record.Status != StatusLive {
			return rollback(fmt.Errorf("create session %q: callback returned status %q, expected %q", id, record.Status, StatusLive))
		}
		if _, exists := state.Sessions[id]; exists {
			return rollback(fmt.Errorf("create session %q: registry collision while allocation lock is held", id))
		}
		state.Sessions[id] = record
		writeErr := s.write(s.RegistryPath(), *state)
		if writeErr == nil {
			return nil
		}
		writeErr = fmt.Errorf("record live session %q: %w", id, writeErr)
		rollbackErr := rollback(nil)
		record.Status = StatusStopped
		record.PID = 0
		state.Sessions[id] = record
		reconcileErr := s.write(s.RegistryPath(), *state)
		if reconcileErr != nil {
			reconcileErr = fmt.Errorf("record stopped session %q after live write failure: %w", id, reconcileErr)
		}
		return errors.Join(writeErr, rollbackErr, reconcileErr)
	})
	return id, err
}

func availableSessionID(state *registry, headers map[string]string, now time.Time, random io.Reader) (string, error) {
	for {
		id, err := newSessionID(now, random)
		if err != nil {
			return "", err
		}
		if _, exists := state.Sessions[id]; exists {
			continue
		}
		if _, exists := headers[id]; exists {
			continue
		}
		return id, nil
	}
}

func newSessionID(now time.Time, random io.Reader) (string, error) {
	const suffixLength = 6
	const unbiasedLimit = 252

	suffix := make([]byte, suffixLength)
	buffer := make([]byte, 1)
	for i := range suffix {
		for {
			if _, err := io.ReadFull(random, buffer); err != nil {
				return "", fmt.Errorf("generate session id: %w", err)
			}
			if int(buffer[0]) >= unbiasedLimit {
				continue
			}
			suffix[i] = idAlphabet[int(buffer[0])%len(idAlphabet)]
			break
		}
	}

	return "s-" + now.UTC().Format("20060102") + "-" + string(suffix), nil
}
