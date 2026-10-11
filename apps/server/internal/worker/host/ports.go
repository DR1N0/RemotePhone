package host

import (
	"fmt"
	"net"
	"strings"
)

const (
	DefaultConsolePort = 5600
	DefaultAdbPort     = 5601
	DefaultGRPCPort    = 8554
	maxPortSearchSteps = 100
)

type EmulatorPorts struct {
	Console int
	Adb     int
	GRPC    int
}

func AllocateEmulatorPorts() (EmulatorPorts, error) {
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
