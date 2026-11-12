package connectivity

import (
	"net/netip"
	"testing"
)

func TestAgentScope(t *testing.T) {
	scope := NetworkScope{CIDRs: []string{"10.0.0.0/24", "127.0.0.0/8", "169.254.0.0/16", "::/0"}, Ports: []int{22}}
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "10.0.1.1", "::1", "fe80::1", "ff02::1"} {
		if scope.Allows(netip.MustParseAddr(ip), 22) {
			t.Fatal(ip)
		}
	}
	if !scope.Allows(netip.MustParseAddr("10.0.0.2"), 22) || scope.Allows(netip.MustParseAddr("10.0.0.2"), 23) {
		t.Fatal("scope/port")
	}
	scope.CIDRs = []string{"127.0.0.1/32"}
	if !scope.Allows(netip.MustParseAddr("::ffff:127.0.0.1"), 22) {
		t.Fatal("explicit loopback exception")
	}
}
