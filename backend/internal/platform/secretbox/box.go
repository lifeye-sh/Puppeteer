package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

func Seal(key, plain []byte, aad string) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return g.Seal(n, n, plain, []byte(aad)), nil
}
func Open(key, sealed []byte, aad string) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("invalid ciphertext")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], []byte(aad))
}
func Wrap(key, plain []byte, aad string) (ciphertext, wrapped []byte, err error) {
	dek := make([]byte, 32)
	if _, err = rand.Read(dek); err != nil {
		return
	}
	defer clear(dek)
	if ciphertext, err = Seal(dek, plain, aad); err != nil {
		return
	}
	wrapped, err = Seal(key, dek, "dek:"+aad)
	return
}
func Unwrap(key, ciphertext, wrapped []byte, aad string) ([]byte, error) {
	dek, e := Open(key, wrapped, "dek:"+aad)
	if e != nil {
		return nil, e
	}
	defer clear(dek)
	return Open(dek, ciphertext, aad)
}
