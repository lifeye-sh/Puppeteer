package rdpcert

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestEnciphermentOnlyWindowsCertificate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "rdp-fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDataEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, suite := range []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_256_GCM_SHA384} {
		t.Run(tls.CipherSuiteName(suite), func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			done := make(chan error, 1)
			go func() {
				defer server.Close()
				server.SetDeadline(time.Now().Add(5 * time.Second))
				req := make([]byte, 19)
				if _, e := io.ReadFull(server, req); e != nil {
					done <- e
					return
				}
				if req[15] != 3 {
					done <- fmt.Errorf("unexpected security negotiation")
					return
				}
				if _, e := server.Write([]byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 2, 0, 8, 0, 2, 0, 0, 0}); e != nil {
					done <- e
					return
				}
				peer := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{suite}})
				if e := peer.Handshake(); e != nil {
					done <- e
					return
				}
				if peer.ConnectionState().CipherSuite != suite {
					done <- fmt.Errorf("wrong suite")
					return
				}
				buf := make([]byte, 1)
				n, e := peer.Read(buf)
				if n != 0 || e != io.EOF {
					done <- fmt.Errorf("discovery sent application data or failed to close: n=%d err=%v", n, e)
					return
				}
				done <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cert, e := Probe(ctx, client, "rdp-fixture")
			client.Close()
			if e != nil {
				t.Fatal(e)
			}
			if cert.Fingerprint != fmt.Sprintf("sha256:%x", sha256.Sum256(der)) {
				t.Fatal("fingerprint mismatch")
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestDiscoveryTLSBoundary(t *testing.T) {
	c := discoveryTLSConfig("rdp-fixture")
	if c.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS minimum changed")
	}
	for _, s := range c.CipherSuites {
		switch s {
		case tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_256_GCM_SHA384:
			continue
		}
		secure := false
		for _, v := range tls.CipherSuites() {
			if v.ID == s {
				secure = true
			}
		}
		if !secure {
			t.Fatalf("unexpected legacy suite %x", s)
		}
	}
}
