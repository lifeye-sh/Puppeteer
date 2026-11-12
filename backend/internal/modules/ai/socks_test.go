package ai

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestR16SOCKSProxyPinsBothHops(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("model secret missing")
		}
		fmt.Fprint(w, `{"data":[{"id":"fixture"}]}`)
	}))
	defer target.Close()
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			var calls atomic.Int32
			done := make(chan struct{})
			go func() {
				defer close(done)
				local, err := listener.Accept()
				if err != nil {
					return
				}
				defer local.Close()
				_ = local.SetDeadline(time.Now().Add(5 * time.Second))
				greeting := make([]byte, 3)
				if _, err = io.ReadFull(local, greeting); err != nil || string(greeting) != "\x05\x01\x00" {
					t.Error("unexpected SOCKS authentication")
					return
				}
				_, _ = local.Write([]byte{5, 0})
				header := make([]byte, 4)
				if _, err = io.ReadFull(local, header); err != nil || string(header) != "\x05\x01\x00\x01" {
					t.Error("destination must be a validated IPv4 literal")
					return
				}
				address := make([]byte, 6)
				if _, err = io.ReadFull(local, address); err != nil {
					t.Error(err)
					return
				}
				remote, err := net.DialTimeout("tcp", net.JoinHostPort(net.IP(address[:4]).String(), fmt.Sprint(binary.BigEndian.Uint16(address[4:]))), time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				defer remote.Close()
				calls.Add(1)
				_, _ = local.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				copyDone := make(chan struct{})
				go func() { io.Copy(remote, local); remote.Close(); close(copyDone) }()
				io.Copy(local, remote)
				local.Close()
				<-copyDone
			}()
			profile := Profile{BaseURL: target.URL + "/v1", APIKey: "fixture", AllowPrivate: true, ProviderOptions: ProviderOptions{ProxyURL: scheme + "://" + listener.Addr().String()}}
			models, err := Models(context.Background(), profile)
			if err != nil || len(models) != 1 || models[0] != "fixture" {
				t.Fatal(models, err)
			}
			profile.BaseURL = "http://169.254.169.254"
			if _, err = Models(context.Background(), profile); err == nil {
				t.Error("metadata access allowed")
			}
			profile.BaseURL = target.URL
			profile.AllowPrivate = false
			if _, err = Models(context.Background(), profile); err == nil {
				t.Error("private address allowed without opt-in")
			}
			if calls.Load() != 1 {
				t.Error("unexpected proxy connection")
			}
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Fatal("proxy connection leaked")
			}
		})
	}
}
