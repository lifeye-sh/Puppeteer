package identity

import (
	"bytes"
	"testing"
	"time"
)

func TestRFC6238SixDigitVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		at   int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if got := TOTP(secret, v.at/30); got != v.want {
			t.Fatalf("%d: %s != %s", v.at, got, v.want)
		}
	}
}
func TestMFAEncryptionAndReplay(t *testing.T) {
	s := &Service{key: bytes.Repeat([]byte{42}, 32)}
	plain := []byte("secret material")
	a, e := s.cryptSeal("user-a", plain)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := s.cryptSeal("user-a", plain)
	if bytes.Equal(a, b) || bytes.Contains(a, plain) {
		t.Fatal("nonce reuse or plaintext")
	}
	if _, e = s.cryptOpen("user-b", a); e == nil {
		t.Fatal("cross-user cipher accepted")
	}
	a[len(a)-1] ^= 1
	if _, e = s.cryptOpen("user-a", a); e == nil {
		t.Fatal("tampered cipher accepted")
	}
	step := time.Now().Unix() / 30
	if validStep(plain, TOTP(plain, step), step) != -1 {
		t.Fatal("same step replay")
	}
}
