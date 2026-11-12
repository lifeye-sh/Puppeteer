package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"testing"
)

func TestPersonalPublicKeyValidation(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewPublicKey(pub)
	text := string(ssh.MarshalAuthorizedKey(key))
	normalized, fp, algorithm, e := ValidatePublicKey(text)
	if e != nil || fp != ssh.FingerprintSHA256(key) || algorithm != ssh.KeyAlgoED25519 || normalized == "" {
		t.Fatal("valid Ed25519 rejected", e)
	}
	for _, value := range []string{"", text + text, "command=\"whoami\" " + text, "-----BEGIN OPENSSH PRIVATE KEY-----\ninvalid"} {
		if _, _, _, e := ValidatePublicKey(value); e == nil {
			t.Fatal("invalid public key accepted")
		}
	}
}
