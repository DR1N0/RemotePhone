package host

import (
	"net"
	"strconv"
	"testing"
)

func TestParseEmulatorPorts(t *testing.T) {
	ports, err := parseEmulatorPorts("5600,5601,8554")
	if err != nil {
		t.Fatalf("parseEmulatorPorts: %v", err)
	}
	if ports.Console != 5600 || ports.Adb != 5601 || ports.GRPC != 8554 {
		t.Errorf("ports = %+v, want 5600/5601/8554", ports)
	}
}

func TestParseEmulatorPortsRejectsBadInput(t *testing.T) {
	cases := []string{
		"5600,5601",           // missing grpc
		"5600,5601,8554,9000", // too many
		"5600,5602,8554",      // adb not console+1
		"5601,5602,8554",      // odd console
		"nope,5601,8554",      // non-numeric
		"0,1,8554",            // zero port
	}
	for _, raw := range cases {
		if _, err := parseEmulatorPorts(raw); err == nil {
			t.Errorf("parseEmulatorPorts(%q) succeeded, want error", raw)
		}
	}
}

func TestAllocateEmulatorPortsHonorsControlPlaneAssignment(t *testing.T) {
	t.Setenv(emulatorPortsEnv, "5700,5701,8570")
	ports, err := AllocateEmulatorPorts()
	if err != nil {
		t.Fatalf("AllocateEmulatorPorts: %v", err)
	}
	if ports.Console != 5700 || ports.Adb != 5701 || ports.GRPC != 8570 {
		t.Errorf("ports = %+v, want assigned 5700/5701/8570", ports)
	}
}

func TestAllocateEmulatorPortsRejectsUnavailableAssignedPort(t *testing.T) {
	// Bind an even console port so the assigned triple is provably in use.
	var listener net.Listener
	var port int
	for attempt := 0; attempt < 20; attempt++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		if p%2 == 0 {
			listener, port = l, p
			break
		}
		_ = l.Close()
	}
	if listener == nil {
		t.Skip("could not bind an even ephemeral port")
	}
	defer listener.Close()

	t.Setenv(emulatorPortsEnv, strconv.Itoa(port)+","+strconv.Itoa(port+1)+",8571")
	if _, err := AllocateEmulatorPorts(); err == nil {
		t.Error("AllocateEmulatorPorts succeeded on an in-use assigned console port, want error")
	}
}
