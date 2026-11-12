package vault

import (
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"io"
	"strings"
	"testing"
)

func TestRotationPasswordNeverAppearsInCommand(t *testing.T) {
	password := "test-only-long-random-password"
	command, stdin, e := PasswordRotationCommand("tester", password, true)
	if e != nil {
		t.Fatal(e)
	}
	if command != "sudo -n chpasswd" || strings.Contains(command, password) {
		t.Fatal("secret in command")
	}
	raw, _ := io.ReadAll(stdin)
	if string(raw) != "tester:"+password+"\n" {
		t.Fatal("invalid chpasswd framing")
	}
	for _, bad := range []string{"root\nother", "-root", "root;id", "../root"} {
		if _, _, e = PasswordRotationCommand(bad, password, false); e == nil {
			t.Fatal("invalid account accepted")
		}
	}
	if _, _, e = PasswordRotationCommand("tester", password+"\nroot:injected", false); e == nil {
		t.Fatal("line injection")
	}
}
func TestRotationKeysPreserveRestrictionsAndOtherKeys(t *testing.T) {
	key := func() string {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		signer, _ := ssh.NewSignerFromKey(priv)
		return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	}
	old, newKey, other := key(), key(), key()
	options := `from="192.0.2.0/24",no-port-forwarding,command="echo restricted"`
	existing := []byte("# retained\n" + options + " " + old + " old-comment\n" + other + " other-comment\n")
	staged, e := ReplaceAuthorizedKey(existing, old, newKey, false)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(staged), options+" "+newKey) || !strings.Contains(string(staged), old) || !strings.Contains(string(staged), other+" other-comment") {
		t.Fatal("restrictions or unrelated key changed")
	}
	committed, e := ReplaceAuthorizedKey(staged, old, newKey, true)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(committed), old) || !strings.Contains(string(committed), options+" "+newKey) {
		t.Fatal("invalid key commit")
	}
	if _, e = ReplaceAuthorizedKey(existing, old, newKey, true); e == nil {
		t.Fatal("removed old key without candidate")
	}
	if _, e = ReplaceAuthorizedKey(staged, old, newKey, false); e == nil {
		t.Fatal("blind replay allowed")
	}
	tampered := strings.Replace(string(staged), options+" "+newKey, newKey, 1)
	if _, e = ReplaceAuthorizedKey([]byte(tampered), old, newKey, true); e == nil {
		t.Fatal("candidate restrictions changed before commit")
	}
}
