package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"golang.org/x/crypto/ssh"
	"net"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
	"strings"
	"testing"
	"time"
)

func TestR16GatewayHostKeyRotationUsesActiveGeneration(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.config.NativeSSHAddr = "127.0.0.1:0"
	a.listenerManager = listener.New(ctx, 8)
	t.Cleanup(func() { a.listenerManager.Close(); a.listenerManager.Wait() })
	if _, e := a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	original, e := a.effectiveGatewayPublicKey(ctx)
	if e != nil {
		t.Fatal(e)
	}
	observed := func() string {
		t.Helper()
		raw, e := net.DialTimeout("tcp", a.effectiveListenerAddress("ssh"), time.Second)
		if e != nil {
			t.Fatal(e)
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(3 * time.Second))
		var key string
		_, _, _, _ = ssh.NewClientConn(raw, "isolated-gateway", &ssh.ClientConfig{User: "no-auth-fixture", HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
			return errors.New("stop after verified test key observation")
		}})
		if key == "" {
			t.Fatal("SSH handshake returned no host key")
		}
		return key
	}
	if observed() != original {
		t.Fatal("initial advertised key differs from wire")
	}
	pub, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		t.Fatal(e)
	}
	rawKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	public, e := ssh.NewPublicKey(pub)
	if e != nil {
		t.Fatal(e)
	}
	expected := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
	save := func(changes map[string]any, clearKeys []string, password string, want int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": changes, "clearSecrets": clearKeys, "password": password}, admin)
		expect(t, r, want)
		if strings.Contains(string(r.body), rawKey) {
			t.Fatal("private key returned")
		}
	}
	save(map[string]any{"ssh-server-private-key": rawKey}, nil, "", 422)
	save(map[string]any{"ssh-server-private-key": "not-a-private-key"}, nil, testPassword, 422)
	save(map[string]any{"ssh-server-private-key": "garbage\n" + rawKey}, nil, testPassword, 422)
	save(map[string]any{"ssh-server-private-key": rawKey + rawKey}, nil, testPassword, 422)
	save(map[string]any{"ssh-server-private-key": rawKey}, nil, testPassword, 202)
	if before, _ := a.effectiveGatewayPublicKey(ctx); before != original {
		t.Fatal("requested key advertised before listener switch")
	}
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if got, _ := a.effectiveGatewayPublicKey(ctx); got != expected || observed() != expected {
		t.Fatal("new key not used by advertised and real SSH identities")
	}
	var retained string
	if e = pool.QueryRow(ctx, `SELECT public_key FROM native_ssh_gateway_keys WHERE singleton=1`).Scan(&retained); e != nil || retained != original {
		t.Fatal("custom key overwrote automatic host identity", e)
	}
	// A failed listener revision must not advertise the requested fallback key.
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	save(map[string]any{"ssh-server-addr": occupied.Addr().String()}, []string{"ssh-server-private-key"}, testPassword, 202)
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal(e)
	}
	if got, _ := a.effectiveGatewayPublicKey(ctx); got != expected {
		t.Fatal("failed switch changed advertised fingerprint")
	}
	save(map[string]any{"ssh-server-addr": ""}, nil, "", 202)
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if got, _ := a.effectiveGatewayPublicKey(ctx); got != original || observed() != original {
		t.Fatal("explicit clear did not restore original platform key")
	}
}
