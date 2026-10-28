package androidstudio

import (
	"errors"
	"fmt"

	"remotephone/internal/worker/runtime"
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

// processIsRunnable is the platform-neutral liveness check; the actual OS
// probe lives in process_unix.go / process_windows.go (Windows has no
// signal-0 or `ps`, so this cannot be a single portable implementation).
func processIsRunnable(pid int) (bool, error) {
	if pid <= 0 {
		return false, errors.New("emulator process not running")
	}
	running, err := processRunning(pid)
	if err != nil {
		return false, fmt.Errorf("inspect emulator process state: %w", err)
	}
	if !running {
		return false, errors.New("emulator process not running")
	}
	return true, nil
}
