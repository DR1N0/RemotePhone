package host

import "net"

// IsAdvertisableIPv4 reports whether ip should be offered as a WebRTC host
// candidate. The policy is deliberately policy-free: every routable IPv4
// interface is advertised, including LAN, Tailscale (100.64/10), and other
// VPNs. ICE exists to choose among multiple interfaces, so hard-coding one
// "LAN" address (or special-casing Tailscale) defeats the mechanism and
// makes the other deployment mode unreachable.
//
// Excluded: IPv6 (the worker currently configures UDP4 only), link-local
// (169.254/16, not routable), multicast/unspecified. Loopback is kept so a
// local probe/test can connect without a LAN interface.
func IsAdvertisableIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.To4() == nil {
		return false // IPv6 is not gathered while network types are UDP4-only
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return ip.IsGlobalUnicast()
}
