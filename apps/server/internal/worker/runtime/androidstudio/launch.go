package androidstudio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"remotephone/internal/worker/host"
	"remotephone/internal/worker/runtime"
)

type LaunchRequest struct {
	AVDName     string
	ConsolePort int
	AdbPort     int
	GRPCPort    int
	Headless    bool
	ReadOnly    bool
	WipeData    bool
	EnableGRPC  bool
	GPUMode     string
	ExtraArgs   []string
}

// BuildLaunchArgs deliberately does NOT support redirecting an AVD's
// writable state via -datadir/-init-data: confirmed by direct testing
// against a real emulator build (36.4.9.0) that both are silently ignored
// (-datadir) or rejected outright (-init-data: "unknown option") once
// -avd <name> is given — those flags only apply to the emulator's legacy
// non-AVD raw-image invocation mode, not the -avd mode this codebase uses.
// Per-instance isolation is instead achieved one level up, by giving each
// instance its own independently-registered AVD (see
// Adapter.ensureInstanceAVD) — AVDName here is already that per-instance
// name, not the shared template's.
func BuildLaunchArgs(req LaunchRequest) []string {
	args := []string{
		"-avd", req.AVDName,
		"-ports", fmt.Sprintf("%d,%d", req.ConsolePort, req.AdbPort),
	}
	if req.Headless {
		args = append(args, "-no-window")
	}
	if req.ReadOnly {
		args = append(args, "-read-only")
	}
	if req.WipeData {
		args = append(args, "-wipe-data")
	}
	if req.EnableGRPC {
		args = append(args, "-grpc", strconv.Itoa(req.GRPCPort))
	}
	if req.GPUMode != "" {
		args = append(args, "-gpu", req.GPUMode)
	}
	args = append(args, req.ExtraArgs...)
	return args
}

func Launch(emulatorPath string, req LaunchRequest) (*exec.Cmd, error) {
	cmd := exec.Command(emulatorPath, BuildLaunchArgs(req)...)
	logPath, err := os.CreateTemp("", "remotephone-emulator-*.log")
	if err == nil {
		cmd.Stdout = logPath
		cmd.Stderr = logPath
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func BuildLaunchRequestFromStart(req runtime.StartRequest, ports host.EmulatorPorts, avdName string) LaunchRequest {
	return LaunchRequest{
		AVDName:     avdName,
		ConsolePort: ports.Console,
		AdbPort:     ports.Adb,
		GRPCPort:    ports.GRPC,
		Headless:    req.LaunchConfig.Headless,
		ReadOnly:    req.LaunchConfig.ReadOnly,
		WipeData:    req.LaunchConfig.WipeData,
		EnableGRPC:  true,
	}
}
