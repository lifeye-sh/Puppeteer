package oauthkeys

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
)

var ErrKey = errors.New("invalid OAuth key")

func Signing(raw []byte) (*rsa.PrivateKey, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return nil, ErrKey
	}
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
		return nil, ErrKey
	}
	block, rest := pem.Decode(trimmed)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return nil, ErrKey
	}
	var key *rsa.PrivateKey
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			key, _ = parsed.(*rsa.PrivateKey)
		}
	default:
		return nil, ErrKey
	}
	clear(block.Bytes)
	if err != nil || key == nil || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || key.E != 65537 || key.Validate() != nil {
		return nil, ErrKey
	}
	return key, nil
}
func HMAC(text string) ([]byte, error) {
	key, e := base64.RawURLEncoding.Strict().DecodeString(text)
	if e != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != text {
		clear(key)
		return nil, ErrKey
	}
	return key, nil
}
