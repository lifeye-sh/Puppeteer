package listener

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func echoSpec(address, identity string) Spec {
	return Spec{Name: "test", Address: address, Identity: identity, Handle: func(ctx context.Context, c net.Conn) {
		s := bufio.NewScanner(c)
		for s.Scan() {
			if _, err := io.WriteString(c, identity+":"+s.Text()+"\n"); err != nil {
				return
			}
		}
	}}
}
func TestPrepareSwitchPreservesConnectionsAndRotatesIdentity(t *testing.T) {
	m := New(context.Background(), 8)
	t.Cleanup(func() { m.Close(); m.Wait() })
	prepare := func(spec Spec) *Prepared {
		t.Helper()
		p, e := m.Prepare(context.Background(), []Spec{spec})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	if e := prepare(echoSpec("127.0.0.1:0", "key-one")).Commit(); e != nil {
		t.Fatal(e)
	}
	first := m.States()[0].Address
	dial := func(address string) net.Conn {
		t.Helper()
		c, e := net.DialTimeout("tcp", address, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	check := func(c net.Conn, expected string) {
		t.Helper()
		c.SetDeadline(time.Now().Add(time.Second))
		io.WriteString(c, "probe\n")
		line, e := bufio.NewReader(c).ReadString('\n')
		if e != nil || line != expected+":probe\n" {
			t.Fatal(line, e)
		}
	}
	old := dial(first)
	check(old, "key-one")
	// Same address swaps the cryptographic handler for new connections only.
	if e := prepare(echoSpec("127.0.0.1:0", "key-two")).Commit(); e != nil {
		t.Fatal(e)
	}
	if m.States()[0].Address != first {
		t.Fatal("same address rebound")
	}
	check(old, "key-one")
	check(dial(first), "key-two")
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	if p, e := m.Prepare(context.Background(), []Spec{echoSpec(occupied.Addr().String(), "bad-key")}); e == nil {
		p.Abort()
		t.Fatal("occupied endpoint prepared")
	}
	check(dial(first), "key-two")
	// Bind a second endpoint before closing the first; existing streams drain.
	reserved, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	second := reserved.Addr().String()
	reserved.Close()
	p := prepare(echoSpec(second, "key-three"))
	check(dial(first), "key-two")
	if e = p.Commit(); e != nil {
		t.Fatal(e)
	}
	check(dial(second), "key-three")
	check(old, "key-one")
	if c, e := net.DialTimeout("tcp", first, time.Second); e == nil {
		c.Close()
		t.Fatal("old listener still accepting")
	}
	if e = prepare(Spec{Name: "test"}).Commit(); e != nil {
		t.Fatal(e)
	}
	if len(m.States()) != 0 {
		t.Fatal("disable did not close listener")
	}
	check(old, "key-one")
	m.Close()
	old.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = old.Read(make([]byte, 1)); e == nil {
		t.Fatal("shutdown left stream open")
	}
}

func TestPreparedRollbackStaleAndCancelled(t *testing.T) {
	m := New(context.Background(), 1)
	defer func() { m.Close(); m.Wait() }()
	p, e := m.Prepare(context.Background(), []Spec{echoSpec("127.0.0.1:0", "a")})
	if e != nil {
		t.Fatal(e)
	}
	address := p.created[0].listener.Addr().String()
	p.Abort()
	p.Abort()
	l, e := net.Listen("tcp", address)
	if e != nil {
		t.Fatal("aborted port leaked", e)
	}
	l.Close()
	p, e = m.Prepare(context.Background(), []Spec{echoSpec("127.0.0.1:0", "a")})
	if e != nil {
		t.Fatal(e)
	}
	q, e := m.Prepare(context.Background(), []Spec{echoSpec("127.0.0.1:0", "b")})
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = q.Commit(); e != ErrStale {
		t.Fatal("stale prepare replaced listener", e)
	}
	if m.States()[0].Identity != "a" {
		t.Fatal(m.States())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, e = m.Prepare(ctx, []Spec{echoSpec("127.0.0.1:0", "c")}); e == nil {
		p.Abort()
		t.Fatal("cancelled prepare accepted")
	}
}
