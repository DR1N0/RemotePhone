package host

import (
	"net"
	"testing"
)

func TestIsAdvertisableIPv4IncludesLANAndTailscale(t *testing.T) {
	for _, raw := range []string{
		"192.168.1.7",
		"10.0.0.5",
		"172.16.4.2",
		"100.99.102.55", // Tailscale / RFC 6598 CGNAT range: routable on the tailnet
		"8.8.8.8",
		"127.0.0.1", // local probes
	} {
		if !IsAdvertisableIPv4(net.ParseIP(raw)) {
			t.Errorf("IsAdvertisableIPv4(%s) = false, want true", raw)
		}
	}
}

func TestIsAdvertisableIPv4ExcludesNonRoutable(t *testing.T) {
	for _, raw := range []string{
		"169.254.10.1",      // link-local
		"::1",               // IPv6 loopback is not gathered while UDP4-only
		"fd7a:115c:a1e0::1", // Tailscale IPv6; excluded with UDP4-only
		"224.0.0.251",       // multicast
		"0.0.0.0",           // unspecified
	} {
		if IsAdvertisableIPv4(net.ParseIP(raw)) {
			t.Errorf("IsAdvertisableIPv4(%s) = true, want false", raw)
		}
	}
}
