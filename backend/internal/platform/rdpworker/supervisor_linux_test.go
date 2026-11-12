//go:build linux

package rdpworker

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerHelper(t *testing.T) {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[len(os.Args)-1], "worker-") {
		return
	}
	mode := os.Args[len(os.Args)-1]
	init := os.NewFile(4, "init")
	data, e := io.ReadAll(init)
	init.Close()
	if e != nil || len(data) != 32 {
		os.Exit(8)
	}
	if mode == "worker-control" {
		c, err := net.Dial("unix", os.Args[len(os.Args)-2])
		if err != nil {
			os.Exit(8)
		}
		c.Write(data)
		ack := make([]byte, 1)
		if _, err = io.ReadFull(c, ack); err != nil || ack[0] != 1 {
			os.Exit(8)
		}
		c.Close()
	}
	if mode == "worker-stall" {
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	ready := os.NewFile(5, "ready")
	if mode == "worker-invalid" {
		ready.Write([]byte("WRONG\n"))
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	ready.Write([]byte("READY\n"))
	ready.Close()
	if mode == "worker-crash" {
		os.Exit(7)
	}
	sock := os.NewFile(3, "socket")
	conn, e := net.FileConn(sock)
	sock.Close()
	if e != nil {
		os.Exit(8)
	}
	if mode == "worker-hold" {
		signal.Ignore(syscall.SIGTERM)
		conn.Write([]byte("active"))
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	b := make([]byte, 4)
	if _, e = io.ReadFull(conn, b); e != nil {
		os.Exit(8)
	}
	conn.Write(b)
	conn.Close()
	os.Exit(0)
}

func TestSupervisorControlHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.SetDeadline(time.Now().Add(3 * time.Second))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sup, err := New(exe, []string{"-test.run=^TestWorkerHelper$", path, "worker-control"}, 1, 2*time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, client := pair(t)
	client.SetDeadline(time.Now().Add(4 * time.Second))
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, s) }()
	c, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	nonce := make([]byte, 32)
	if _, err = io.ReadFull(c, nonce); err != nil {
		t.Fatal(err)
	}
	if err = sup.AuthorizeControl(c, nonce); err != nil {
		t.Fatal(err)
	}
	if sup.AuthorizeControl(c, nonce) == nil {
		t.Fatal("worker proof replay accepted")
	}
	c.Write([]byte{1})
	client.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err = io.ReadFull(client, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("handoff: %q %v", buf, err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not exit")
	}
}

func pair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	c, e := net.DialTCP("tcp4", nil, l.Addr().(*net.TCPAddr))
	if e != nil {
		t.Fatal(e)
	}
	s, e := l.AcceptTCP()
	if e != nil {
		c.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close(); s.Close() })
	return s, c
}

func TestSupervisor(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"echo", "stall", "invalid", "crash", "hold"} {
		t.Run(mode, func(t *testing.T) {
			sup, e := New(exe, []string{"-test.run=^TestWorkerHelper$", "worker-" + mode}, 1, 200*time.Millisecond, 100*time.Millisecond)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			server, client := pair(t)
			client.SetDeadline(time.Now().Add(2 * time.Second))
			done := make(chan error, 1)
			go func() { done <- sup.Run(ctx, server) }()
			if mode == "echo" {
				client.Write([]byte("ping"))
				b := make([]byte, 4)
				if _, e = io.ReadFull(client, b); e != nil || string(b) != "ping" {
					t.Fatalf("socket handoff: %q %v", b, e)
				}
			}
			if mode == "hold" {
				b := make([]byte, 6)
				if _, e = io.ReadFull(client, b); e != nil {
					t.Fatal(e)
				}
				other, _ := pair(t)
				if e = sup.Run(ctx, other); !errors.Is(e, ErrCapacity) {
					t.Fatalf("capacity: %v", e)
				}
				cancel()
			}
			select {
			case e = <-done:
				if (mode == "echo") != (e == nil) {
					t.Fatalf("%s: %v", mode, e)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("worker leaked")
			}
			if len(sup.slots) != 0 {
				t.Fatal("slot leaked")
			}
			if mode == "hold" || mode == "echo" {
				b := make([]byte, 1)
				if _, e = client.Read(b); e != io.EOF {
					t.Fatalf("socket not closed: %v", e)
				}
			}
		})
	}
}

func TestSupervisorAdmissionCleanup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-program", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			path := exe
			if mode == "missing-program" {
				path = "/nonexistent/nt-rdp-worker"
			}
			sup, err := New(path, nil, 1, time.Second, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			server, client := pair(t)
			if err = sup.Run(ctx, server); err == nil {
				t.Fatal("invalid admission accepted")
			}
			client.SetReadDeadline(time.Now().Add(time.Second))
			if _, err = client.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("connection leaked: %v", err)
			}
			if len(sup.slots) != 0 {
				t.Fatal("capacity leaked")
			}
		})
	}
}
