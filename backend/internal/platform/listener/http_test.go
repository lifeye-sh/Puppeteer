package listener

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPUpgradeDrainsAcrossSwitchAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, 8)
	defer func() { m.Close(); m.Wait() }()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upgrade" {
			io.WriteString(w, "ok")
			return
		}
		c, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: fixture\r\n\r\n")
		rw.Flush()
		io.Copy(c, rw)
	})
	apply := func(address string) {
		t.Helper()
		p, e := m.Prepare(ctx, []Spec{{Name: "http", Address: address, Handle: HTTP(h, nil, true)}})
		if e != nil {
			t.Fatal(e)
		}
		defer p.Abort()
		if e = p.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	apply("127.0.0.1:0")
	old := m.States()[0].Address
	c, e := net.DialTimeout("tcp", old, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(c, "GET /upgrade HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: fixture\r\n\r\n")
	br := bufio.NewReader(c)
	resp, e := http.ReadResponse(br, nil)
	if e != nil || resp.StatusCode != 101 {
		t.Fatal(resp, e)
	}
	port, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	next := port.Addr().String()
	port.Close()
	apply(next)
	if probe, e := net.DialTimeout("tcp", old, time.Second); e == nil {
		probe.Close()
		t.Fatal("old accept socket retained")
	}
	fmt.Fprint(c, "retained\n")
	if got, e := br.ReadString('\n'); e != nil || got != "retained\n" {
		t.Fatal("upgraded connection interrupted", got, e)
	}
	client := http.Client{Timeout: time.Second}
	v, e := client.Get("http://" + next)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(v.Body)
	v.Body.Close()
	client.CloseIdleConnections()
	if string(data) != "ok" {
		t.Fatal(string(data))
	}
	cancel()
	if _, e = br.ReadByte(); e == nil {
		t.Fatal("shutdown retained upgraded connection")
	}
}

func TestHTTPPreservesTLSStateAndTrust(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := certServer.TLS.Certificates[0]
	leaf := certServer.Certificate()
	certServer.Close()
	m := New(context.Background(), 4)
	defer func() { m.Close(); m.Wait() }()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			http.Error(w, "missing TLS", 500)
			return
		}
		io.WriteString(w, "tls")
	})
	p, e := m.Prepare(context.Background(), []Spec{{Name: "tls", Address: "127.0.0.1:0", Handle: HTTP(h, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, false)}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Abort()
	if e = p.Commit(); e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: time.Second}
	v, e := client.Get("https://" + m.States()[0].Address)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(v.Body)
	v.Body.Close()
	if string(data) != "tls" {
		t.Fatal(string(data))
	}
}
