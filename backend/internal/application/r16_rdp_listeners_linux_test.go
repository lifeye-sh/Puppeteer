//go:build linux

package application

import (
	"context"
	"errors"
	"net"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
	"os"
	"testing"
)

func TestR16ManagedRDPValidationAndCertificateFailure(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	root := t.TempDir()
	_, cert, key, _ := r14ProbeCertificate(t, root, "front", false)
	a.ConfigureNativeRDP(NativeRDPOptions{Address: "127.0.0.1:0", Worker: "/bin/true", FFmpeg: "/bin/true", Certificate: cert, PrivateKey: key})
	a.listenerManager = listener.New(ctx, 4)
	defer func() { a.listenerManager.Close(); a.listenerManager.Wait() }()
	v, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	old := a.nativeRDPAddress.Load()
	if old == nil {
		t.Fatal("no managed RDP endpoint")
	}
	before := a.listenerManager.States()[0]
	if e = os.WriteFile(key, []byte("broken private key"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal("invalid key accepted", e)
	}
	if got := a.nativeRDPAddress.Load(); got == nil || *got != *old {
		t.Fatal("failed key replaced endpoint")
	}
	if after := a.listenerManager.States()[0]; after != before {
		t.Fatal("failed key switched generation")
	}
	// Certificate error must not be bypassed by publishing a new wrong hostname.
	_, cert2, key2, _ := r14ProbeCertificate(t, root, "front2", false)
	a.managedRDP.Certificate = cert2
	a.managedRDP.PrivateKey = key2
	v.Values["native-rdp-public-address"] = "other.invalid:3389"
	if _, e = a.managedRDPSpec(v.Values); e == nil {
		t.Fatal("wrong advertised SAN accepted")
	}
	v.Values["native-rdp-public-address"] = ""
	v.Values["native-rdp-addr"] = "0.0.0.0:3389"
	if _, e = a.managedRDPSpec(v.Values); e == nil {
		t.Fatal("non-loopback deployment widened")
	}
	v.Values["native-rdp-addr"] = ""
	_, expiredCert, expiredKey, _ := r14ProbeCertificate(t, root, "expired", true)
	a.managedRDP.Certificate = expiredCert
	a.managedRDP.PrivateKey = expiredKey
	if _, e = a.managedRDPSpec(v.Values); e == nil {
		t.Fatal("expired certificate accepted")
	}
	current, _ := a.system.Configuration(ctx, 0)
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(current.ID), map[string]any{"set": map[string]any{"native-rdp-enabled": false}}, admin)
	expect(t, r, 202)
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if a.nativeRDPAddress.Load() != nil {
		t.Fatal("disabled endpoint advertised")
	}
	if c, e := net.Dial("tcp", *old); e == nil {
		c.Close()
		t.Fatal("disabled listener accepts")
	}
}
