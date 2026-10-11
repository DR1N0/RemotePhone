package androidstudio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"mako/internal/worker/host"
	"mako/internal/worker/runtime"
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
	ExtraArgs   []string
}

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
	args = append(args, req.ExtraArgs...)
	return args
}

func Launch(emulatorPath string, req LaunchRequest) (*exec.Cmd, error) {
	cmd := exec.Command(emulatorPath, BuildLaunchArgs(req)...)
	logPath, err := os.CreateTemp("", "mako-emulator-*.log")
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
