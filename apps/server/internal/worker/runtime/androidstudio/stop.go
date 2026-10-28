package androidstudio

import (
	"errors"
	"fmt"
	"os/exec"
	"time"

	"remotephone/internal/worker/runtime"
)

// emulatorShutdownTimeout bounds how long stopEmulator waits for the
// emulator to exit after `adb emu kill` before force-killing its process
// tree. A real emulator shutdown is normally a few seconds; a hung one
// must not block instance stop forever.
const emulatorShutdownTimeout = 15 * time.Second

// StopProcess is the low-level "terminate this OS process" primitive. It is
// platform-specific (SIGTERM on Unix, taskkill on Windows) and is only the
// fallback path — stopEmulator is what instance stop normally uses.
func StopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("emulator process not running")
	}
	return stopProcess(pid)
}

// stopEmulator shuts an instance's emulator down the way the emulator itself
// expects to be stopped: through the console command `adb -s
// emulator-<consolePort> emu kill`, which flushes guest state and lets the
// AVD's snapshot/data files settle. If adb isn't available, the emulator
// doesn't respond within emulatorShutdownTimeout, or the handle has no
// console port, it falls back to killing the process directly.
//
// This matters on Windows especially: os.Process.Signal only supports Kill
// there, so the old SIGTERM-based path could never stop an emulator at all.
// Doing adb-first on every platform also gives Unix a cleaner shutdown than
// the bare SIGTERM it used before.
func stopEmulator(handle runtime.InstanceHandle) error {
	if handle.ConsolePort > 0 {
		serial := fmt.Sprintf("emulator-%d", handle.ConsolePort)
		// Only wait for a graceful exit when adb accepted the command; if
		// adb is missing or the console is gone, go straight to the
		// process-kill fallback instead of burning the full timeout.
		if err := exec.Command("adb", "-s", serial, "emu", "kill").Run(); err == nil {
			if waitForProcessExit(handle.PID, emulatorShutdownTimeout) {
				return nil
			}
		}
	}
	if handle.PID <= 0 {
		return errors.New("emulator process not running")
	}
	return stopProcess(handle.PID)
}

// waitForProcessExit polls pid until it is gone or timeout elapses. A probe
// error is treated as "not running": the process table couldn't be read,
// which is the closest observable state to "gone" this can act on.
func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		running, err := processRunning(pid)
		if err != nil || !running {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}
