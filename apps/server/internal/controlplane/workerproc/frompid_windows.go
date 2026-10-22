//go:build windows

package workerproc

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// FromPID reconstructs a Process from a bare PID. Unlike POSIX,
// os.FindProcess is a real liveness check on Windows (it calls OpenProcess),
// so a PID that already exited fails with ERROR_INVALID_PARAMETER. That is
// exactly the "already gone" case release-by-PID must treat as success, so
// it becomes a no-op deadProcess rather than an error. Other failures (e.g.
// access denied for a live process) still propagate — silently dropping
// those would leave an orphaned worker running.
func FromPID(pid int) (Process, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return &deadProcess{pid: pid}, nil
		}
		return nil, err
	}
	return &pidProcess{proc: proc}, nil
}
