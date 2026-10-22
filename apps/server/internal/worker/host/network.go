package host

import (
	"net"
	"strings"
)

// DetectLANIP picks the host's most likely LAN IPv4 address for WebRTC host
// candidates. Windows dev boxes routinely have several virtual adapters
// (Tailscale/ZeroTier 100.64/10, Hyper-V/WSL, link-local 169.254/16, VPN
// tunnels), and advertising all of them lets an ICE peer select a path that
// looks "succeeded" but is lossy — observed on an Android emulator client
// choosing a Tailscale candidate and freezing video while the host probe on
// 192.168.1.7 streamed fine.
//
// Preference order: 192.168/16, 10/8, 172.16/12, then anything else public;
// loopback, link-local and CGNAT (100.64/10, Tailscale) are skipped.
func DetectLANIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	bestIP := ""
	bestScore := -1
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			score := lanIPScore(ip)
			if score > bestScore {
				bestIP = ip.String()
				bestScore = score
			}
		}
	}
	return bestIP
}

func lanIPScore(ip net.IP) int {
	ip = ip.To4()
	if ip == nil {
		return -1
	}
	switch {
	case ip[0] == 192 && ip[1] == 168:
		return 4
	case ip[0] == 10:
		return 3
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
		return 2
	case ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127:
		// RFC 6598 carrier-grade NAT; Tailscale uses this range.
		return -1
	case ip[0] == 169 && ip[1] == 254:
		return -1
	default:
		return 1
	}
}

// IsVirtualAdapterName reports whether a network interface name looks like a
// VPN/tunnel/virtual switch that should not be advertised to LAN clients.
// Interface names are locale-dependent on Windows, so this is a best-effort
// secondary filter; the IP-based score above is the primary one.
func IsVirtualAdapterName(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"tailscale", "zerotier", "vethernet", "hyper-v", "wsl", "vmware", "virtualbox", "tun", "tap", "wireguard", "openvpn", "bluetooth"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
