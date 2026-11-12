package netproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestCONNECTAuthenticationAndTunnel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "private.internal:22" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")) {
			w.WriteHeader(407)
			return
		}
		c, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		_, _ = b.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = b.Flush()
		_, _ = io.Copy(c, c)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword("u", "p")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, e := Dial(ctx, u, "private.internal:22")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_, _ = c.Write([]byte("hello"))
	raw := make([]byte, 5)
	if _, e = io.ReadFull(c, raw); e != nil || string(raw) != "hello" {
		t.Fatal(e)
	}
	u.User = url.UserPassword("u", "wrong")
	if _, e = Dial(ctx, u, "private.internal:22"); e == nil {
		t.Fatal("proxy auth bypass")
	}
}
func TestSOCKS5HAuthAndRemoteDNS(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		r := bufio.NewReader(c)
		head := make([]byte, 2)
		_, e = io.ReadFull(r, head)
		if e != nil {
			done <- e
			return
		}
		methods := make([]byte, int(head[1]))
		_, _ = io.ReadFull(r, methods)
		_, _ = c.Write([]byte{5, 2})
		_, _ = io.ReadFull(r, head)
		user := make([]byte, int(head[1]))
		_, _ = io.ReadFull(r, user)
		length, _ := r.ReadByte()
		pass := make([]byte, int(length))
		_, _ = io.ReadFull(r, pass)
		if string(user) != "user" || string(pass) != "secret" {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = c.Write([]byte{1, 0})
		req := make([]byte, 4)
		_, _ = io.ReadFull(r, req)
		if req[3] != 3 {
			done <- io.ErrUnexpectedEOF
			return
		}
		length, _ = r.ReadByte()
		host := make([]byte, int(length))
		_, _ = io.ReadFull(r, host)
		_, _ = io.ReadFull(r, head)
		if string(host) != "unresolvable.internal" {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 22})
		_, _ = io.Copy(c, c)
		done <- nil
	}()
	u, _ := url.Parse("socks5h://user:secret@" + ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := Dial(ctx, u, "unresolvable.internal:22")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = c.Write([]byte("ok"))
	raw := make([]byte, 2)
	_, e = io.ReadFull(c, raw)
	c.Close()
	if e != nil || string(raw) != "ok" {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
