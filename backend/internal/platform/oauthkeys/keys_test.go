package oauthkeys

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestSigningFormatsAndBoundaries(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	for _, raw := range [][]byte{pkcs1, pkcs8, append([]byte(" \n"), pkcs8...)} {
		actual, e := Signing(raw)
		if e != nil || actual.N.Cmp(key.N) != 0 {
			t.Fatal("valid RSA rejected", e)
		}
	}
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecDER, _ := x509.MarshalPKCS8PrivateKey(ec)
	for _, raw := range [][]byte{nil, bytes.Repeat([]byte("x"), 8193), append(append([]byte{}, pkcs1...), pkcs8...), append(append([]byte{}, pkcs1...), []byte("trailing")...), append([]byte("prefix"), pkcs1...), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(weak)}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: x509.MarshalPKCS1PrivateKey(key)})} {
		if _, e := Signing(raw); e == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
func TestHMACCanonicalEncoding(t *testing.T) {
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	encoded := base64.RawURLEncoding.EncodeToString(key)
	actual, e := HMAC(encoded)
	if e != nil || !bytes.Equal(actual, key) {
		t.Fatal("roundtrip", e)
	}
	for _, v := range []string{"", encoded + "=", encoded + "\n", encoded[:42], base64.RawURLEncoding.EncodeToString(key[:31]), base64.RawURLEncoding.EncodeToString(append(key, 0))} {
		if _, e := HMAC(v); e == nil {
			t.Fatal("noncanonical key accepted")
		}
	}
}
