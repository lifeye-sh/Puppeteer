package mysqlproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/server"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTLSFullAuthenticationNeverUsesFastCache(t *testing.T) {
	certificateServer := httptest.NewTLSServer(nil)
	defer certificateServer.Close()
	pool := x509.NewCertPool()
	pool.AddCert(certificateServer.Certificate())
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	var verified atomic.Int32
	var enabled atomic.Bool
	enabled.Store(true)
	password := strings.Repeat("A", 43)
	protocol := NewServer(certificateServer.TLS, func(_ context.Context, user, secret string) error {
		verified.Add(1)
		if !enabled.Load() || user != "reader@inventory" || secret != password {
			return errors.New("denied")
		}
		return nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, e := listener.Accept()
			if e != nil {
				return
			}
			connection.SetDeadline(time.Now().Add(5 * time.Second))
			c, e := protocol.NewCustomizedConn(connection, &IdentityHandler{Ready: func(*server.Conn) error { return nil }}, server.EmptyHandler{})
			if e == nil {
				c.Close()
			} else {
				connection.Close()
			}
		}
	}()
	connect := func(secret string, tlsEnabled bool) error {
		options := []client.Option{}
		if tlsEnabled {
			options = append(options, func(c *client.Conn) error {
				c.SetTLSConfig(&tls.Config{RootCAs: pool, ServerName: "example.com", MinVersion: tls.VersionTLS12})
				return nil
			})
		}
		c, e := client.Connect(listener.Addr().String(), "reader@inventory", secret, "", options...)
		if e == nil {
			c.Close()
		}
		return e
	}
	for i := 0; i < 2; i++ {
		if e = connect(password, true); e != nil {
			t.Fatal(e)
		}
	}
	if verified.Load() != 2 {
		t.Fatal("full authentication was skipped", verified.Load())
	}
	enabled.Store(false)
	if connect(password, true) == nil {
		t.Fatal("revoked credential accepted")
	}
	if connect(strings.Repeat("B", 43), true) == nil {
		t.Fatal("wrong credential accepted")
	}
	before := verified.Load()
	if connect(password, false) == nil {
		t.Fatal("plaintext connection accepted")
	}
	if verified.Load() != before {
		t.Fatal("non-TLS password verification reached")
	}
	listener.Close()
	<-done
}
