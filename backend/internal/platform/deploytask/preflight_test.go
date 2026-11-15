package deploytask

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestPreflightDoesNotPublishOrConsumeGeneration(t *testing.T) {
	d := &fakeDriver{}
	e, err := Open(context.Background(), t.TempDir(), d)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Preflight(context.Background(), Request{Protocol: "mysql"}); err != nil {
		t.Fatal(err)
	}
	generation, task := e.View()
	if generation != 0 || task != nil {
		t.Fatal(generation, task)
	}
	if err = e.Preflight(context.Background(), Request{Protocol: "mysql", Generation: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	d.fail = "preflight"
	if err = e.Preflight(context.Background(), Request{Protocol: "mysql"}); err == nil {
		t.Fatal("failed dependency passed")
	}
	generation, task = e.View()
	if generation != 0 || task != nil {
		t.Fatal(generation, task)
	}
}
func TestHostPortProbe(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if err = probeHostPort(context.Background(), "127.0.0.1", port); err == nil {
		t.Fatal("occupied port accepted")
	}
	if err = probeHostPort(context.Background(), "127.0.0.1", 0); err != nil {
		t.Fatal(err)
	}
}
func TestCancelledStartDoesNotPersist(t *testing.T) {
	e, err := Open(context.Background(), t.TempDir(), &fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.Start(ctx, Request{Protocol: "mysql"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	generation, task := e.View()
	if generation != 0 || task != nil {
		t.Fatal(generation, task)
	}
}
