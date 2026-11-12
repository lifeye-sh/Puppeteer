package vault

import (
	"bytes"
	"encoding/json"
	"golang.org/x/crypto/ssh"
	"testing"
)

func TestRotationCandidateEncryptionAndTargetBinding(t *testing.T) {
	s := New(nil, bytes.Repeat([]byte{42}, 32))
	for _, kind := range []string{"password", "private-key"} {
		v, e := s.NewRotationCandidate(kind, "target-one")
		if e != nil {
			t.Fatal(e)
		}
		secret, e := s.OpenRotationCandidate("target-one", v.Sealed)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.OpenRotationCandidate("target-two", v.Sealed); e == nil {
			t.Fatal("candidate transplanted to another target")
		}
		other := New(nil, bytes.Repeat([]byte{43}, 32))
		if _, e = other.OpenRotationCandidate("target-one", v.Sealed); e == nil {
			t.Fatal("wrong key accepted")
		}
		encoded, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if kind == "password" {
			if len(secret.Password) < 40 || bytes.Contains(encoded, []byte(secret.Password)) || bytes.Contains(v.Sealed, []byte(secret.Password)) {
				t.Fatal("password generation or serialization")
			}
		} else {
			signer, e := ssh.ParsePrivateKey([]byte(secret.PrivateKey))
			if e != nil {
				t.Fatal(e)
			}
			if string(ssh.MarshalAuthorizedKey(signer.PublicKey())) != v.PublicKey {
				t.Fatal("candidate key pair mismatch")
			}
			if bytes.Contains(encoded, []byte("PRIVATE KEY")) {
				t.Fatal("private key exposed")
			}
		}
	}
}
