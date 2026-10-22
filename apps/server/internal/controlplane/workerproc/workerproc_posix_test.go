//go:build !windows

package workerproc

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestFromPIDAndKillTerminatesARealProcess exercises the POSIX path: a real
// child process, reconstructed from its bare PID, must be killed and be
// unsignalable afterwards. Windows uses OpenProcess/GetExitCodeProcess
// semantics instead (see process_windows.go), so this specific signal-0
// assertion doesn't apply there.
func TestFromPIDAndKillTerminatesARealProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	proc, err := FromPID(pid)
	if err != nil {
		t.Fatalf("FromPID: %v", err)
	}
	if proc.PID() != pid {
		t.Errorf("proc.PID() = %d, want %d", proc.PID(), pid)
	}

	// Kill's own goroutine reaps the child via proc.Wait() — don't also
	// call cmd.Wait() here, which would race the same reap and can observe
	// ECHILD ("no child processes") depending on which wins.
	if err := Kill(proc, time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("process %d still signalable after Kill, want it gone", pid)
	}
}
