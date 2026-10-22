//go:build windows

package androidstudio

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// stillActive is winbase.h's STILL_ACTIVE (259), the exit code
// GetExitCodeProcess reports for a process that hasn't terminated.
const stillActive = 259

// processRunning reports whether pid is a live process. Windows has no
// signal-0 probe: OpenProcess + GetExitCodeProcess is the supported way to
// distinguish alive from exited, and it also rejects a PID that never
// existed (ERROR_INVALID_PARAMETER) instead of pretending it is alive.
func processRunning(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil // no such process
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			// Exists, just not ours to query (e.g. a system process); the
			// PID being alive is the only fact this function needs.
			return true, nil
		}
		return false, fmt.Errorf("open emulator process: %w", err)
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, fmt.Errorf("get emulator process exit code: %w", err)
	}
	return exitCode == stillActive, nil
}

// stopProcess force-terminates the process tree rooted at pid. This is the
// fallback path; stopEmulator first asks the emulator to shut down cleanly
// through adb, which is what keeps the instance's AVD data consistent.
func stopProcess(pid int) error {
	output, err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput()
	if err != nil {
		message := string(output)
		// taskkill exits non-zero for an already-dead PID; that is success
		// for a "make sure it is gone" operation, same as POSIX ESRCH.
		if strings.Contains(message, "not found") ||
			strings.Contains(message, "没有找到") ||
			strings.Contains(message, "找不到") {
			return nil
		}
		return fmt.Errorf("taskkill %d: %w (%s)", pid, err, strings.TrimSpace(message))
	}
	return nil
}
