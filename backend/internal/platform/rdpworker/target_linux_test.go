//go:build linux

package rdpworker

import (
	"context"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestTargetBridgeIsolationAndLifetime(t *testing.T) {
	for _, mode := range []string{"echo", "wrong-pid", "wrong-uid", "expired", "cancel", "dial-failure"} {
		t.Run(mode, func(t *testing.T) {
			root, e := os.MkdirTemp("", "nt-rdp-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.Remove(root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			peer := WorkerIdentity{PID: int32(os.Getpid()), UID: uint32(os.Geteuid())}
			if mode == "wrong-pid" {
				peer.PID++
			}
			if mode == "wrong-uid" {
				peer.UID++
			}
			expiry := time.Now().Add(2 * time.Second)
			if mode == "expired" {
				expiry = time.Now().Add(100 * time.Millisecond)
			}
			var dials atomic.Int32
			b, e := NewTargetBridge(ctx, root, peer, expiry, func(ctx context.Context) (net.Conn, error) {
				dials.Add(1)
				if mode == "dial-failure" {
					return nil, io.ErrClosedPipe
				}
				server, client := net.Pipe()
				go func() { defer server.Close(); io.Copy(server, server) }()
				return client, nil
			})
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			if mode == "expired" || mode == "cancel" {
				if mode == "cancel" {
					cancel()
				}
				select {
				case <-b.Done():
				case <-time.After(time.Second):
					t.Fatal("did not close")
				}
				if dials.Load() != 0 {
					t.Fatal("dialed without peer")
				}
				if _, e = os.Stat(b.Path()); !os.IsNotExist(e) {
					t.Fatal("socket leaked")
				}
				return
			}
			conn, e := net.Dial("unix", b.Path())
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(time.Second))
			if mode == "wrong-pid" || mode == "wrong-uid" || mode == "dial-failure" {
				if _, e = conn.Read(make([]byte, 1)); e != io.EOF {
					t.Fatal("unexpected read")
				}
				if mode != "dial-failure" && dials.Load() != 0 {
					t.Fatal("unauthorized dial")
				}
				return
			}
			if _, e = conn.Write([]byte("echo")); e != nil {
				t.Fatal(e)
			}
			got := make([]byte, 4)
			if _, e = io.ReadFull(conn, got); e != nil || string(got) != "echo" {
				t.Fatalf("echo %q %v", got, e)
			}
			if c, e := net.Dial("unix", b.Path()); e == nil {
				c.Close()
				t.Fatal("second connection accepted")
			}
			if e = b.Renew(time.Now().Add(100 * time.Millisecond)); e != nil {
				t.Fatal(e)
			}
			select {
			case <-b.Done():
			case <-time.After(time.Second):
				t.Fatal("lease did not close active stream")
			}
			if dials.Load() != 1 {
				t.Fatal("multiple target dials")
			}
		})
	}
}
