package website

import (
	"net"
	"net/netip"
	"strings"
)

// ClientIP consumes XFF from right to left only while each current hop is trusted.
// Invalid or overlong forwarding chains fall back to the actual peer.
func ClientIP(remote, xff string, trusted []string) string {
	ip, _ := ClientIPDetails(remote, xff, trusted)
	return ip
}

// The second result is true only when a valid forwarding chain was consumed.
func ClientIPDetails(remote, xff string, trusted []string) (string, bool) {
	host, _, e := net.SplitHostPort(remote)
	if e != nil {
		host = remote
	}
	peer, e := netip.ParseAddr(host)
	if e != nil {
		return host, false
	}
	peer = peer.Unmap()
	contains := func(ip netip.Addr) bool {
		for _, raw := range trusted {
			prefix, e := netip.ParsePrefix(raw)
			if e == nil && prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !contains(peer) || xff == "" || len(xff) > 4096 {
		return peer.String(), false
	}
	parts := strings.Split(xff, ",")
	if len(parts) > 32 {
		return peer.String(), false
	}
	ips := make([]netip.Addr, len(parts))
	for i, part := range parts {
		ip, e := netip.ParseAddr(strings.TrimSpace(part))
		if e != nil || ip.Zone() != "" {
			return peer.String(), false
		}
		ips[i] = ip.Unmap()
	}
	current := peer
	for i := len(ips) - 1; i >= 0; i-- {
		if !contains(current) {
			break
		}
		current = ips[i]
	}
	return current.String(), true
}
