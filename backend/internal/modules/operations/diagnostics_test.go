package operations

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestDiagnosticTargetPolicy(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "169.254.169.254", "fe80::1", "224.0.0.1", "ff02::1", "255.255.255.255"} {
		if _, err := DiagnosticIP(context.Background(), host); err == nil {
			t.Fatal("accepted blocked target", host)
		}
	}
	ip, err := DiagnosticIP(context.Background(), "127.0.0.1")
	if err != nil || !ip.IsLoopback() {
		t.Fatal(ip, err)
	}
}
func TestUnprivilegedPing(t *testing.T) {
	if os.Getenv("NT_R16_ICMP") != "1" {
		t.Skip("set NT_R16_ICMP=1 in the isolated Linux diagnostic profile")
	}
	for _, address := range []string{"127.0.0.1", "::1"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := Ping(ctx, net.ParseIP(address), 1)
		cancel()
		if err != nil {
			t.Fatal(address, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Ping(ctx, net.ParseIP("127.0.0.1"), 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
