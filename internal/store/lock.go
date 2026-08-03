package store

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type directoryLock struct {
	file *os.File
}

var checkoutMutexes sync.Map

func checkoutMutex(path string) *sync.Mutex {
	mutex, _ := checkoutMutexes.LoadOrStore(path, new(sync.Mutex))
	return mutex.(*sync.Mutex)
}

func lockDirectory(path string) (*directoryLock, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open registry lock directory %s: %w", path, err)
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock registry directory %s: %w", path, err)
	}
	return &directoryLock{file: file}, nil
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
