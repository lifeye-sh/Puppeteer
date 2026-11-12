package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTLSClientCertificateAndHeaderBinding(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 || r.Header.Get("X-Entry") != "test" {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	srv.StartTLS()
	defer srv.Close()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	ca := x509.NewCertPool()
	ca.AddCert(parsed)
	srv.TLS.ClientCAs = ca
	srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if e := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	certPath, keyPath := filepath.Join(dir, "client.pem"), filepath.Join(dir, "client.key")
	if e := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); e != nil {
		t.Fatal(e)
	}
	cfg := Config{Endpoint: srv.URL, CAFile: caPath, CertFile: certPath, KeyFile: keyPath, Headers: map[string]string{"X-Entry": "test"}}
	client, e := cfg.Client()
	if e != nil {
		t.Fatal(e)
	}
	res, e := client.Get(srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	cfg.CAFile = ""
	client, e = cfg.Client()
	if e != nil {
		t.Fatal(e)
	}
	if res, e = client.Get(srv.URL); e == nil {
		res.Body.Close()
		t.Fatal("untrusted TLS accepted")
	}
	cfg.Headers = map[string]string{"Authorization": "replacement"}
	if _, e = cfg.Client(); e == nil {
		t.Fatal("reserved header accepted")
	}
}
