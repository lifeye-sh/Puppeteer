package guac

import (
	"context"
	"net"
	"testing"
)

func TestProbeChecksBothPluginsWithoutAssetCredentials(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	seen := make(chan string, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, e := l.Accept()
			if e != nil {
				return
			}
			parts, e := NewReader(c).Read()
			if e == nil && len(parts) == 2 && parts[0] == "select" {
				seen <- parts[1]
				c.Write([]byte(Encode("args", "hostname", "port")))
			}
			c.Close()
		}
	}()
	if err = Probe(context.Background(), l.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if <-seen != "rdp" || <-seen != "vnc" {
		t.Fatal("plugins missing")
	}
}
func TestProbeRejectsTCPOnlyService(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e == nil {
			c.Write([]byte("not a guacamole handshake"))
			c.Close()
		}
	}()
	if Probe(context.Background(), l.Addr().String()) == nil {
		t.Fatal("TCP accepted as protocol readiness")
	}
}
