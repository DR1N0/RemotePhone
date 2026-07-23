package tests

import (
	"testing"

	"mako-worker/internal/config"
	"mako-worker/internal/runtime"
	"mako-worker/internal/runtime/androidstudio"
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
