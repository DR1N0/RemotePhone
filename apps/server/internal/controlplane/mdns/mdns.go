// Package mdns advertises the control plane's client API on the LAN using
// DNS-SD. mDNS is discovery only: it never replaces PIN pairing or the pinned
// TLS certificate.
package mdns

import (
	"fmt"
	"net"
	"strings"

	"github.com/grandcat/zeroconf"

	"mako/internal/controlplane/serverinfo"
)

const serviceType = "_mako._tcp"

// Advertiser owns one registered DNS-SD service. Shutdown is idempotent.
type Advertiser struct {
	server *zeroconf.Server
}

// Start registers the control plane on LAN interfaces. disabled returns a
// nil advertiser without doing anything.
func Start(info *serverinfo.Info, port int, disabled bool) (*Advertiser, error) {
	if disabled {
		return nil, nil
	}
	if info == nil {
		return nil, fmt.Errorf("mDNS requires server info")
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid client API port %d", port)
	}
	server, err := zeroconf.Register(
		instanceName(info),
		serviceType,
		"local.",
		port,
		txtRecords(info),
		lanInterfaces(),
	)
	if err != nil {
		return nil, fmt.Errorf("register mDNS service: %w", err)
	}
	return &Advertiser{server: server}, nil
}

// Shutdown removes the service (sends goodbye records). Safe on nil.
func (a *Advertiser) Shutdown() {
	if a == nil || a.server == nil {
		return
	}
	a.server.Shutdown()
	a.server = nil
}

func instanceName(info *serverinfo.Info) string {
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = "mako"
	}
	short := info.ServerID
	if len(short) > 8 {
		short = short[:8]
	}
	full := fmt.Sprintf("%s (%s)", name, short)
	if len(full) > 60 {
		full = full[:60]
	}
	return full
}

func txtRecords(info *serverinfo.Info) []string {
	return []string{
		"id=" + info.ServerID,
		"name=" + info.Name,
		"v=1",
		"path=/api/v1",
	}
}

// lanInterfaces limits advertisement to private IPv4 LAN interfaces, keeping
// Tailscale (100.64/10), link-local, and loopback out of discovery.
func lanInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, iface := range all {
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil && ip4.IsPrivate() {
				out = append(out, iface)
				break
			}
		}
	}
	return out
}
