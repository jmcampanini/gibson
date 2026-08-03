package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type directoryLock struct {
	file *os.File
}

type processLock struct {
	available chan struct{}
}

var checkoutLocks sync.Map

func checkoutLock(path string) *processLock {
	lock, _ := checkoutLocks.LoadOrStore(path, newProcessLock())
	return lock.(*processLock)
}

func newProcessLock() *processLock {
	lock := &processLock{available: make(chan struct{}, 1)}
	lock.available <- struct{}{}
	return lock
}

func (l *processLock) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.available:
		return nil
	}
}

func (l *processLock) unlock() {
	l.available <- struct{}{}
}

func lockDirectory(ctx context.Context, path string) (*directoryLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("lock registry directory %s: %w", path, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open registry lock directory %s: %w", path, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock registry directory %s: %w", path, err)
		}
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &directoryLock{file: file}, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock registry directory %s: %w", path, err)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, fmt.Errorf("lock registry directory %s: %w", path, ctx.Err())
		case <-timer.C:
		}
	}
}

func (l *directoryLock) close() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock registry directory: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close registry lock directory: %w", closeErr)
	}
	return nil
}
