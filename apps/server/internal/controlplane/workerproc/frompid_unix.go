//go:build !windows

package workerproc

import "os"

// FromPID reconstructs a Process from a bare PID — for releasing a worker
// this process didn't itself Spawn (e.g. after a control-plane restart,
// where only the PID survives, persisted in instances.runtime_handle_json).
// Always succeeds on POSIX (os.FindProcess does no liveness check); a
// stale/already-dead PID surfaces as a harmless no-op from Kill, not an
// error here.
func FromPID(pid int) (Process, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}
	return &pidProcess{proc: proc}, nil
}
