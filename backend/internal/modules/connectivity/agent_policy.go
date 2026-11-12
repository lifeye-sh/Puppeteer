package connectivity

import (
	"context"
	"net"
	"net/netip"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

// Every DNS candidate must be in scope. Dial the validated IP, never the hostname.
func (s NetworkScope) Allows(ip netip.Addr, port int) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	allowedPort := false
	for _, p := range s.Ports {
		if p == port {
			allowedPort = true
		}
	}
	if !allowedPort {
		return false
	}
	for _, raw := range s.CIDRs {
		p, e := netip.ParsePrefix(raw)
		if e == nil && p.Contains(ip) {
			if ip.IsLoopback() && p.Bits() != ip.BitLen() {
				continue
			}
			return true
		}
	}
	return false
}
func (s NetworkScope) DialTarget(ctx context.Context, host string, port int) (net.Conn, string, error) {
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if e != nil {
		return nil, "", e
	}
	if len(ips) == 0 {
		return nil, "", fault.Denied
	}
	for _, ip := range ips {
		if !s.Allows(ip, port) {
			return nil, "", fault.Denied
		}
	}
	ip := ips[0].Unmap().String()
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	return c, ip, e
}
