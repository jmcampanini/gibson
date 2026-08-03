//go:build linux

package pisession

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func listProcesses() ([]processRecord, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err == nil {
			pids = append(pids, pid)
		}
	}
	return readListedProcesses(pids, readProcessRecord)
}

func readListedProcesses(pids []int, read processReadFunc) ([]processRecord, error) {
	records := make([]processRecord, 0, len(pids))
	for _, pid := range pids {
		record, exists, err := read(pid)
		if err != nil {
			if isProcessDisappearance(err) || errors.Is(err, os.ErrPermission) {
				continue
			}
			return nil, err
		}
		if exists {
			records = append(records, record)
		}
	}
	return records, nil
}

func readProcessRecord(pid int) (processRecord, bool, error) {
	contents, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		if isProcessDisappearance(err) {
			return processRecord{}, false, nil
		}
		return processRecord{}, false, fmt.Errorf("read process %d: %w", pid, err)
	}
	closeName := bytes.LastIndexByte(contents, ')')
	if closeName < 0 {
		return processRecord{}, false, fmt.Errorf("parse process %d stat", pid)
	}
	fields := strings.Fields(string(contents[closeName+1:]))
	if len(fields) <= 19 {
		return processRecord{}, false, fmt.Errorf("parse process %d stat fields", pid)
	}
	if fields[0] == "Z" {
		return processRecord{}, false, nil
	}
	ppid, ppidErr := strconv.Atoi(fields[1])
	pgid, pgidErr := strconv.Atoi(fields[2])
	if ppidErr != nil || pgidErr != nil {
		return processRecord{}, false, fmt.Errorf("parse process %d parent and group", pid)
	}
	return processRecord{pid: pid, ppid: ppid, pgid: pgid, started: fields[19]}, true, nil
}

func signalProcessIfOwned(owned processRecord, signal syscall.Signal) error {
	pidfd, err := unix.PidfdOpen(owned.pid, 0)
	if err == nil {
		defer func() { _ = unix.Close(pidfd) }()
		current, exists, readErr := readProcessRecord(owned.pid)
		if readErr != nil {
			return readErr
		}
		if !exists || current.started != owned.started {
			return nil
		}
		if err := unix.PidfdSendSignal(pidfd, unix.Signal(signal), nil, 0); err != nil && !isProcessDisappearance(err) {
			return err
		}
		return nil
	}
	if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) {
		if isProcessDisappearance(err) {
			return nil
		}
		return err
	}

	current, exists, err := readProcessRecord(owned.pid)
	if err != nil {
		return err
	}
	if !exists || current.started != owned.started {
		return nil
	}
	if err := syscall.Kill(owned.pid, signal); err != nil && !isProcessDisappearance(err) {
		return err
	}
	return nil
}
