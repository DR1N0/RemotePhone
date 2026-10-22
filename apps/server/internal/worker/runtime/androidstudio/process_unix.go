//go:build !windows

package androidstudio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// processRunning reports whether pid is a live, non-zombie process. Unix
// exposes signal 0 as a cheap existence probe and `ps` for the zombie state
// Linux/macOS still need distinguished.
func processRunning(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find emulator process: %w", err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return false, nil
	}

	state, err := readProcessState(pid)
	if err != nil {
		return false, fmt.Errorf("inspect emulator process state: %w", err)
	}
	if strings.Contains(state, "Z") {
		return false, nil
	}
	return true, nil
}

func readProcessState(pid int) (string, error) {
	output, err := exec.Command("ps", "-o", "stat=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// stopProcess asks the emulator to terminate with SIGTERM. Callers that know
// the console port should prefer stopEmulator (graceful adb shutdown) and
// only fall back here.
func stopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find emulator process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop emulator process: %w", err)
	}
	return nil
}
