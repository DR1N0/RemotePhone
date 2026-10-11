package tests

import (
	"testing"

	"mako/internal/worker/config"
	"mako/internal/worker/runtime"
	"mako/internal/worker/runtime/androidstudio"
)

func TestAndroidStudioAdapterAvailabilityChecksEmulatorBinary(t *testing.T) {
	adapter := androidstudio.NewAdapter(config.Config{
		WorkerID:            "local",
		HomeDir:             t.TempDir(),
		AndroidEmulatorPath: "/path/that/does/not/exist",
	}, runtime.NewMemoryHandleStore(), nil)

	if err := adapter.IsAvailable(); err == nil {
		t.Fatal("expected availability check to fail when emulator binary is missing")
	}
}
