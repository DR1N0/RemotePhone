package host

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultConsolePort = 5600
	DefaultAdbPort     = 5601
	DefaultGRPCPort    = 8554
	maxPortSearchSteps = 100
)

// emulatorPortsEnv carries a control-plane-assigned "console,adb,grpc"
// triple. The control plane owns host-wide port assignment because every
// worker process starting its own search at DefaultConsolePort produced a
// bind race the moment two instances ran concurrently.
const emulatorPortsEnv = "REMOTEPHONE_EMULATOR_PORTS"

type EmulatorPorts struct {
	Console int
	Adb     int
	GRPC    int
}

func AllocateEmulatorPorts() (EmulatorPorts, error) {
	if requested := os.Getenv(emulatorPortsEnv); requested != "" {
		ports, err := parseEmulatorPorts(requested)
		if err != nil {
			return EmulatorPorts{}, fmt.Errorf("parse %s=%q: %w", emulatorPortsEnv, requested, err)
		}
		for _, port := range []int{ports.Console, ports.Adb, ports.GRPC} {
			if !isTCPPortAvailable(port) {
				return EmulatorPorts{}, fmt.Errorf("emulator port %d from %s is already in use", port, emulatorPortsEnv)
			}
		}
		return ports, nil
	}

	console, adb, err := findConsoleAndAdbPorts(DefaultConsolePort)
	if err != nil {
		return EmulatorPorts{}, err
	}

	grpc, err := findAvailablePort(DefaultGRPCPort, 1)
	if err != nil {
		return EmulatorPorts{}, err
	}

	return EmulatorPorts{
		Console: console,
		Adb:     adb,
		GRPC:    grpc,
	}, nil
}

// parseEmulatorPorts validates the "console,adb,grpc" triple the control
// plane assigned, including the emulator's own console/adb pairing rule
// (adb is always console+1).
func parseEmulatorPorts(raw string) (EmulatorPorts, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return EmulatorPorts{}, fmt.Errorf("want 3 comma-separated ports, got %d", len(parts))
	}
	values := make([]int, 3)
	for i, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value <= 0 || value > 65535 {
			return EmulatorPorts{}, fmt.Errorf("invalid port %q", part)
		}
		values[i] = value
	}
	if values[1] != values[0]+1 {
		return EmulatorPorts{}, fmt.Errorf("adb port %d must be console port %d + 1", values[1], values[0])
	}
	if values[0]%2 != 0 {
		return EmulatorPorts{}, fmt.Errorf("console port %d must be even", values[0])
	}
	return EmulatorPorts{Console: values[0], Adb: values[1], GRPC: values[2]}, nil
}

func findConsoleAndAdbPorts(start int) (int, int, error) {
	port := start
	if port%2 != 0 {
		port++
	}

	for attempt := 0; attempt < maxPortSearchSteps; attempt++ {
		console := port + (attempt * 2)
		adb := console + 1
		if isTCPPortAvailable(console) && isTCPPortAvailable(adb) {
			return console, adb, nil
		}
	}

	return 0, 0, fmt.Errorf("no console/adb port pair available starting at %d", start)
}

func findAvailablePort(start int, step int) (int, error) {
	if step <= 0 {
		step = 1
	}

	for attempt := 0; attempt < maxPortSearchSteps; attempt++ {
		candidate := start + (attempt * step)
		if isTCPPortAvailable(candidate) {
			return candidate, nil
		}
	}

	return 0, fmt.Errorf("no port available starting at %d", start)
}

func isTCPPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func NormalizeGOOS(goos string) string {
	switch strings.ToLower(goos) {
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	default:
		return goos
	}
}
