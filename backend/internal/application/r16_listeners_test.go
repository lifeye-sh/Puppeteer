package application

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
)

func TestR16ListenerPortFailureSwitchAndDrain(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.config.NativeSSHAddr = "127.0.0.1:0"
	a.listenerManager = listener.New(ctx, 8)
	t.Cleanup(func() { a.listenerManager.Close(); a.listenerManager.Wait() })
	if _, e := a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	oldAddress := a.effectiveListenerAddress("ssh")
	old, e := net.DialTimeout("tcp", oldAddress, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	old.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, e := bufio.NewReader(old).ReadString('\n')
	if e != nil || !strings.HasPrefix(line, "SSH-2.0-NextTerminal") {
		t.Fatal(line, e)
	}
	change := func(patch map[string]any) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": patch}, admin)
		expect(t, r, 202)
	}
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	change(map[string]any{"ssh-server-addr": occupied.Addr().String()})
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal("port conflict not reported", e)
	}
	if a.effectiveListenerAddress("ssh") != oldAddress {
		t.Fatal("failed prepare replaced working listener")
	}
	v, e := a.system.ConfigurationView(ctx, 0)
	if e != nil || v.State != "failed" {
		t.Fatal(v.State, e)
	}
	// Freeing the port alone cannot bypass the failed-revision latch.
	address := occupied.Addr().String()
	occupied.Close()
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal(e)
	}
	if e = a.db.Within(ctx, func(c context.Context) error { return a.system.RetryConfiguration(c, v.ID) }); e != nil {
		t.Fatal(e)
	}
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if a.effectiveListenerAddress("ssh") != address {
		t.Fatal("port did not switch")
	}
	if c, e := net.DialTimeout("tcp", oldAddress, time.Second); e == nil {
		c.Close()
		t.Fatal("old port still accepts")
	}
	fresh, e := net.DialTimeout("tcp", address, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	fresh.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, e = bufio.NewReader(fresh).ReadString('\n')
	if e != nil || !strings.HasPrefix(line, "SSH-2.0-NextTerminal") {
		t.Fatal(line, e)
	}
	// The earlier connection is waiting for its client handshake, rather than
	// being cancelled when its listening socket is replaced.
	old.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, e = old.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(e, &timeout) || !timeout.Timeout() {
		t.Fatal("established connection interrupted by switch", e)
	}
	change(map[string]any{"ssh-server-enabled": false})
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if a.effectiveListenerAddress("ssh") != "" || a.nativeSSHReady.Load() {
		t.Fatal("disabled listener advertised")
	}
}

func TestR16ListenerTLSCertificateRotation(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.config.PostgresProxyAddr = "127.0.0.1:0"
	a.listenerManager = listener.New(ctx, 8)
	t.Cleanup(func() { a.listenerManager.Close(); a.listenerManager.Wait() })
	if _, e := a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	address := a.effectiveListenerAddress("postgres")
	initialID, _ := a.databaseProxyCertificate.Load().(string)
	initial, e := a.certificates().Get(ctx, initialID)
	if e != nil {
		t.Fatal(e)
	}
	connect := func(pem string) *tls.Conn {
		t.Helper()
		raw, e := net.DialTimeout("tcp", address, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { raw.Close() })
		raw.SetDeadline(time.Now().Add(5 * time.Second))
		request := make([]byte, 8)
		binary.BigEndian.PutUint32(request, 8)
		binary.BigEndian.PutUint32(request[4:], 80877103)
		if _, e = raw.Write(request); e != nil {
			t.Fatal(e)
		}
		response := make([]byte, 1)
		if _, e = io.ReadFull(raw, response); e != nil || response[0] != 'S' {
			t.Fatal("TLS negotiation", e)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(pem)) {
			t.Fatal("fixture CA")
		}
		conn := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
		if e = conn.HandshakeContext(ctx); e != nil {
			t.Fatal(e)
		}
		return conn
	}
	old := connect(initial.PEM)
	store := func(host string) (string, string) {
		t.Helper()
		cert, key, e := pki.Generate("rotation fixture", []string{host}, 7, false, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer clear(key)
		var id string
		e = a.db.Within(ctx, func(c context.Context) error {
			var e error
			id, e = a.certificates().Store(c, pki.Certificate{Name: "rotation fixture", Kind: "server", Source: "self_signed", PEM: string(cert)}, key)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return id, string(cert)
	}
	id, pem := store("localhost")
	change := func(id string) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"db-proxy-certificate-id": id}}, admin)
		expect(t, r, 202)
	}
	change(id)
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if a.effectiveListenerAddress("postgres") != address {
		t.Fatal("TLS rotation rebound port")
	}
	fresh := connect(pem)
	if fresh.ConnectionState().PeerCertificates[0].SerialNumber.Cmp(old.ConnectionState().PeerCertificates[0].SerialNumber) == 0 {
		t.Fatal("old TLS certificate reused")
	}
	old.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, e = old.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(e, &timeout) || !timeout.Timeout() {
		t.Fatal("TLS rotation terminated established stream", e)
	}
	wrongID, _ := store("wrong.fixture.invalid")
	change(wrongID)
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal("wrong certificate hostname accepted", e)
	}
	if current, _ := a.databaseProxyCertificate.Load().(string); current != id {
		t.Fatal("failed certificate advertised")
	}
	if a.effectiveListenerAddress("postgres") != address {
		t.Fatal("failed certificate removed listener")
	}
}
