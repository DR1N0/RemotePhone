package androidstudio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"mako/internal/worker/runtime"
)

func InspectHandle(store runtime.HandleStore, runtimeClass string, instanceID string) (runtime.InstanceStatus, error) {
	handle, ok := store.Get(instanceID)
	if !ok {
		return runtime.InstanceStatus{}, runtime.ErrInstanceNotFound
	}
	if handle.RuntimeClass != runtimeClass {
		return runtime.InstanceStatus{}, runtime.ErrInstanceNotFound
	}

	state := "running"
	lastError := ""
	running, err := processIsRunnable(handle.PID)
	if err != nil {
		state = "failed"
		lastError = err.Error()
	} else if !running {
		state = "failed"
		lastError = "emulator process not running"
	}

	return runtime.InstanceStatus{
		State:     state,
		Handle:    handle,
		LastError: lastError,
	}, nil
}

func ReconcileHandles(store runtime.HandleStore) ([]runtime.InstanceStatus, error) {
	handles := store.List()
	statuses := make([]runtime.InstanceStatus, 0, len(handles))
	for _, handle := range handles {
		if handle.RuntimeClass != RuntimeClass {
			continue
		}
		status, err := InspectHandle(store, RuntimeClass, handle.InstanceID)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func processIsRunnable(pid int) (bool, error) {
	if pid <= 0 {
		return false, errors.New("emulator process not running")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find emulator process: %w", err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return false, errors.New("emulator process not running")
	}

	state, err := readProcessState(pid)
	if err != nil {
		return false, fmt.Errorf("inspect emulator process state: %w", err)
	}
	if state == "" {
		return false, errors.New("emulator process not running")
	}
	if strings.Contains(state, "Z") {
		return false, errors.New("emulator process is not runnable")
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
