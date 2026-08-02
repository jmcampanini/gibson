//go:build darwin

package pisession

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

const darwinZombieProcessState = 5

func listProcesses() ([]processRecord, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	records := make([]processRecord, 0, len(processes))
	for _, process := range processes {
		if process.Proc.P_stat != darwinZombieProcessState {
			records = append(records, processRecordFromKinfo(process))
		}
	}
	return records, nil
}

func readProcessRecord(pid int) (processRecord, bool, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
			return processRecord{}, false, nil
		}
		return processRecord{}, false, fmt.Errorf("read process %d: %w", pid, err)
	}
	if process == nil || int(process.Proc.P_pid) != pid || process.Proc.P_stat == darwinZombieProcessState {
		return processRecord{}, false, nil
	}
	return processRecordFromKinfo(*process), true, nil
}

func processRecordFromKinfo(process unix.KinfoProc) processRecord {
	started := process.Proc.P_starttime
	return processRecord{
		pid:     int(process.Proc.P_pid),
		ppid:    int(process.Eproc.Ppid),
		pgid:    int(process.Eproc.Pgid),
		started: fmt.Sprintf("%d:%d", started.Sec, started.Usec),
	}
}

func signalProcessIfOwned(owned processRecord, signal syscall.Signal) error {
	current, exists, err := readProcessRecord(owned.pid)
	if err != nil {
		return err
	}
	if !exists || current.started != owned.started {
		return nil
	}
	if err := syscall.Kill(owned.pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
