package vault

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"testing"
)

func TestEncryptedSSHKeyValidation(t *testing.T) {
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	block, e := ssh.MarshalPrivateKeyWithPassphrase(key, "fixture", []byte("key-passphrase"))
	if e != nil {
		t.Fatal(e)
	}
	in := Write{Credential: Credential{Name: "key", Kind: "private-key", Username: "remote"}, Secret: &Secret{PrivateKey: string(pem.EncodeToMemory(block)), Passphrase: "key-passphrase"}}
	if e = Validate(&in); e != nil || in.PublicKey == "" {
		t.Fatal("valid encrypted key rejected", e)
	}
	in.Secret.Passphrase = "wrong"
	if e = Validate(&in); e == nil {
		t.Fatal("wrong passphrase accepted")
	}
}
