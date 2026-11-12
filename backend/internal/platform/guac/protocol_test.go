package guac

import (
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestFraming(t *testing.T) {
	want := []string{"name", "中文;,.😀"}
	got, e := NewReader(strings.NewReader(Encode(want...))).Read()
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, e)
	}
	for _, s := range []string{".x;", "1.x!", "99999999.x;", "1.\xff;", "0.;", "262145.x;"} {
		if _, e := NewReader(strings.NewReader(s)).Read(); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	r := NewReader(strings.NewReader("3.nop;3.nop;"))
	for i := 0; i < 2; i++ {
		if _, e := r.Read(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestReadonlyAndChannels(t *testing.T) {
	for _, p := range [][]string{{"key", "65", "1"}, {"mouse", "1", "2", "1"}, {"size", "800", "600"}} {
		if InputAllowed(p, true) || !InputAllowed(p, false) {
			t.Fatal(p)
		}
	}
	for _, p := range [][]string{{"clipboard", "1", "text/plain"}, {"blob", "1", "QQ=="}, {"file", "1", "x", "x"}, {"key", "-1", "1"}, {"size", "9000", "700"}, {"select", "rdp"}} {
		if InputAllowed(p, false) {
			t.Fatal(p)
		}
	}
	if !InputAllowed([]string{"sync", "123"}, true) {
		t.Fatal("sync blocked")
	}
}
func TestHandshakeKeepsBufferedOutput(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	done := make(chan error, 1)
	go func() {
		defer b.Close()
		r := NewReader(b)
		p, e := r.Read()
		if e != nil || p[0] != "select" {
			done <- ErrProtocol
			return
		}
		io.WriteString(b, Encode("args", "VERSION_1_5_0", "hostname", "password"))
		for i := 0; i < 4; i++ {
			if _, e = r.Read(); e != nil {
				done <- e
				return
			}
		}
		p, e = r.Read()
		if e != nil || len(p) != 4 || p[1] != "VERSION_1_1_0" || p[3] != "secret" {
			done <- ErrProtocol
			return
		}
		_, e = io.WriteString(b, Encode("ready", "id")+Encode("sync", "42"))
		done <- e
	}()
	r, e := Handshake(a, "vnc", map[string]string{"hostname": "relay", "password": "secret"})
	if e != nil {
		t.Fatal(e)
	}
	p, e := r.Read()
	if e != nil || p[0] != "sync" {
		t.Fatal(p, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
