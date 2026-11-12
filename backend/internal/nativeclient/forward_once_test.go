package nativeclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"testing"
	"time"
)

func TestForwardOnce(t *testing.T) {
	for _, mode := range []string{"echo", "reset", "rejected", "cancel", "disconnect", "idle-cancel", "idle-disconnect"} {
		t.Run(mode, func(t *testing.T) {
			reject := mode == "rejected"
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			signer, _ := ssh.NewSignerFromKey(key)
			config := &ssh.ServerConfig{NoClientAuth: true}
			config.AddHostKey(signer)
			server, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			accepted := make(chan struct{})
			go func() {
				conn, e := server.Accept()
				if e != nil {
					return
				}
				peer, channels, requests, e := ssh.NewServerConn(conn, config)
				if e != nil {
					conn.Close()
					return
				}
				defer peer.Close()
				go ssh.DiscardRequests(requests)
				for ch := range channels {
					close(accepted)
					if reject {
						ch.Reject(ssh.Prohibited, "denied")
						continue
					}
					stream, requests, e := ch.Accept()
					if e != nil {
						return
					}
					go ssh.DiscardRequests(requests)
					go func() { defer stream.Close(); io.Copy(stream, stream) }()
				}
			}()
			peer, err := ssh.Dial("tcp", server.Addr().String(), &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().String()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- ServeForwardOnce(ctx, peer, listener, "fixture:22") }()
			if mode == "idle-cancel" || mode == "idle-disconnect" {
				if mode == "idle-cancel" {
					cancel()
				} else {
					peer.Close()
				}
				select {
				case e := <-done:
					if e == nil {
						t.Fatal("idle termination reported success")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("idle listener not closed")
				}
				conn, e := net.DialTimeout("tcp", address, time.Second)
				if e == nil {
					conn.Close()
					t.Fatal("listener leaked")
				}
				return
			}
			local, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			local.SetDeadline(time.Now().Add(3 * time.Second))
			select {
			case <-accepted:
			case <-ctx.Done():
				t.Fatal("channel not opened")
			}
			second, e := net.DialTimeout("tcp", address, time.Second)
			if e == nil {
				second.Close()
				t.Fatal("listener allowed reuse")
			}
			if !reject {
				if _, err = local.Write([]byte("hello")); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 5)
				if _, err = io.ReadFull(local, buf); err != nil || string(buf) != "hello" {
					t.Fatalf("echo %q: %v", buf, err)
				}
				if mode == "reset" {
					local.(*net.TCPConn).SetLinger(0)
					local.Close()
				} else if mode == "cancel" {
					cancel()
				} else if mode == "disconnect" {
					peer.Close()
				} else {
					local.(*net.TCPConn).CloseWrite()
				}
				if _, err = local.Read(make([]byte, 1)); mode != "reset" && err != io.EOF {
					t.Fatalf("expected EOF: %v", err)
				}
			}
			local.Close()
			select {
			case err = <-done:
				if (mode == "rejected" || mode == "cancel" || mode == "disconnect" || mode == "reset") && err == nil {
					t.Fatal("expected failure to propagate")
				}
				if mode == "echo" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("forward did not exit")
			}
		})
	}
}
