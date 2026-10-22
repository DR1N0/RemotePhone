package host

import (
	"net"
	"testing"
)

func TestLANIPScorePrefersPrivateLANRanges(t *testing.T) {
	cases := map[string]int{
		"192.168.1.7":  4,
		"10.0.0.5":     3,
		"172.16.4.2":   2,
		"172.31.255.1": 2,
		"8.8.8.8":      1,
		"100.99.1.1":   -1, // Tailscale / RFC 6598
		"169.254.10.1": -1, // link-local
		"127.0.0.1":    1,  // loopback is filtered before scoring
	}
	for raw, want := range cases {
		if got := lanIPScore(net.ParseIP(raw)); got != want {
			t.Errorf("lanIPScore(%s) = %d, want %d", raw, got, want)
		}
	}
}

func TestIsVirtualAdapterName(t *testing.T) {
	for _, name := range []string{"Tailscale", "vEthernet (WSL)", "ZeroTier One", "Bluetooth Network Connection"} {
		if !IsVirtualAdapterName(name) {
			t.Errorf("IsVirtualAdapterName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"WLAN", "Ethernet", "以太网"} {
		if IsVirtualAdapterName(name) {
			t.Errorf("IsVirtualAdapterName(%q) = true, want false", name)
		}
	}
}

func TestDetectLANIPReturnsUsableIPv4(t *testing.T) {
	ip := DetectLANIP()
	if ip == "" {
		t.Skip("no non-loopback IPv4 interface on this host")
	}
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		t.Fatalf("DetectLANIP() = %q, not an IPv4 address", ip)
	}
	if parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
		t.Errorf("DetectLANIP() = %q, want a routable LAN address", ip)
	}
}
