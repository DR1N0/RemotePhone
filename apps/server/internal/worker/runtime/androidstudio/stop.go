package androidstudio

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func StopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("emulator process not running")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find emulator process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop emulator process: %w", err)
	}

	return nil
}
