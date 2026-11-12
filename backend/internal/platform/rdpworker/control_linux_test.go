//go:build linux

package rdpworker

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlPeerBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	client, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	conn, e := listener.AcceptUnix()
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	nonce := bytes.Repeat([]byte{'s'}, 32)
	pid, uid := int32(os.Getpid()), uint32(os.Geteuid())
	for _, mode := range []string{"unknown", "wrong-pid", "wrong-uid", "expired", "wrong-nonce", "revoked", "valid"} {
		t.Run(mode, func(t *testing.T) {
			g := &controlGate{}
			boundPID, boundUID, expires := pid, uid, time.Now().Add(time.Second)
			if mode == "wrong-pid" {
				boundPID++
			}
			if mode == "wrong-uid" {
				boundUID++
			}
			if mode == "expired" {
				expires = time.Now().Add(-time.Second)
			}
			if mode != "unknown" {
				g.register(boundPID, boundUID, nonce, expires)
			}
			if mode == "revoked" {
				g.revoke(pid)
			}
			proof := append([]byte(nil), nonce...)
			if mode == "wrong-nonce" {
				proof[0]++
			}
			err := g.consume(conn, proof)
			if (mode == "valid") != (err == nil) {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "valid" && g.consume(conn, proof) == nil {
				t.Fatal("replay accepted")
			}
		})
	}
	g := &controlGate{}
	g.register(pid, uid, nonce, time.Now().Add(time.Second))
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.consume(conn, nonce) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent consumes: %d", successes.Load())
	}
}

func TestControlFactoryRequiresProof(t *testing.T) {
	for _, valid := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "control.sock")
		listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if e != nil {
			t.Fatal(e)
		}
		client, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
		if e != nil {
			t.Fatal(e)
		}
		server, e := listener.AcceptUnix()
		if e != nil {
			t.Fatal(e)
		}
		listener.Close()
		s := &Supervisor{}
		nonce := bytes.Repeat([]byte{'n'}, 32)
		s.control.register(int32(os.Getpid()), uint32(os.Geteuid()), nonce, time.Now().Add(time.Second), "198.51.100.1")
		var got WorkerIdentity
		calls := 0
		done := make(chan error, 1)
		go func() {
			done <- s.ServeControlFactory(context.Background(), server, func(v WorkerIdentity) ControlBackend { got = v; calls++; return &controlFixture{ttl: time.Second} })
		}()
		if !valid {
			nonce[0]++
		}
		client.Write(nonce)
		client.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("control did not finish")
		}
		if !valid && calls != 0 {
			t.Fatal("factory ran before proof")
		}
		if valid && (calls != 1 || got.PID != int32(os.Getpid()) || got.UID != uint32(os.Geteuid()) || got.SourceIP != "198.51.100.1" || len(got.ID) != 36) {
			t.Fatal("worker identity not bound")
		}
	}
}
