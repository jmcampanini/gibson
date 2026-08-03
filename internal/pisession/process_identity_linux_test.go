//go:build linux

package pisession

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadProcessRecordTreatsMissingProcEntryAsDisappearance(t *testing.T) {
	_, exists, err := readProcessRecord(1 << 30)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestReadListedProcessesContinuesPastPerPIDChurn(t *testing.T) {
	kept := processRecord{pid: 14, ppid: 1, pgid: 14, started: "kept"}
	read := func(pid int) (processRecord, bool, error) {
		switch pid {
		case 11:
			return processRecord{}, false, &os.PathError{Op: "read", Path: "/proc/11/stat", Err: syscall.ENOENT}
		case 12:
			return processRecord{}, false, syscall.ESRCH
		case 13:
			return processRecord{}, false, syscall.EACCES
		case kept.pid:
			return kept, true, nil
		default:
			return processRecord{}, false, nil
		}
	}

	records, err := readListedProcesses([]int{11, 12, 13, kept.pid}, read)
	require.NoError(t, err)
	assert.Equal(t, []processRecord{kept}, records)
}

func TestReadListedProcessesPreservesGenuineReadFailure(t *testing.T) {
	readErr := syscall.EIO
	_, err := readListedProcesses([]int{11}, func(int) (processRecord, bool, error) {
		return processRecord{}, false, readErr
	})
	require.ErrorIs(t, err, readErr)
	assert.False(t, errors.Is(err, syscall.ENOENT))
}
