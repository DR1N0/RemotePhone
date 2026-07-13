package tests

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"mako-worker/internal/runtime"
	"mako-worker/internal/runtime/androidstudio"
)

func TestAndroidStudioInspectHandleRejectsForeignRuntimeOwnership(t *testing.T) {
	store := runtime.NewMemoryHandleStore()
	store.Put(runtime.InstanceHandle{
		InstanceID:   "instance-foreign",
		RuntimeClass: "other-runtime",
		PID:          1234,
	})

	_, err := androidstudio.InspectHandle(store, androidstudio.RuntimeClass, "instance-foreign")
	if err != runtime.ErrInstanceNotFound {
		t.Fatalf("expected ErrInstanceNotFound for foreign runtime handle, got %v", err)
	}
}

func TestAndroidStudioInspectHandleFailsForZombieProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start short-lived process: %v", err)
	}
	defer func() {
		_ = cmd.Wait()
	}()

	if !waitForProcessState(cmd.Process.Pid, "Z", 2*time.Second) {
		t.Fatalf("process %d did not enter zombie state in time", cmd.Process.Pid)
	}

	store := runtime.NewMemoryHandleStore()
	store.Put(runtime.InstanceHandle{
		InstanceID:   "instance-zombie",
		RuntimeClass: androidstudio.RuntimeClass,
		PID:          cmd.Process.Pid,
	})

	status, err := androidstudio.InspectHandle(store, androidstudio.RuntimeClass, "instance-zombie")
	if err != nil {
		t.Fatalf("inspect handle: %v", err)
	}
	if status.State != "failed" {
		t.Fatalf("expected failed state for zombie process, got %q", status.State)
	}
	if status.LastError == "" {
		t.Fatal("expected last error for zombie process")
	}
}

func waitForProcessState(pid int, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := readProcessState(pid)
		if err == nil && strings.Contains(state, want) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func readProcessState(pid int) (string, error) {
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
