package entitlements

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestLocalProviderIsolationAndDefaults(t *testing.T) {
	now := time.Unix(1800000000, 0)
	p, e := NewLocal(nil)
	if e != nil {
		t.Fatal(e)
	}
	s, e := p.Snapshot(fixtureInstance, now)
	if e != nil || !s.Allows("assets.hosts") || s.Quotas.Users != nil || !s.AllowImplemented {
		t.Fatal(s, e)
	}
	p, e = NewLocal([]byte(`{"features":{"assets.hosts":true,"database.postgres":false},"quotas":{"users":10,"resources":null,"sessions":0}}`))
	if e != nil {
		t.Fatal(e)
	}
	s, e = p.Snapshot(fixtureInstance, now)
	if e != nil || !s.Allows("assets.hosts") || s.Allows("database.postgres") || s.Allows("unknown.feature") || s.Quotas.Users == nil || *s.Quotas.Users != 10 {
		t.Fatal(s, e)
	}
	s.Features["assets.hosts"] = false
	*s.Quotas.Users = 9999
	next, e := p.Snapshot(fixtureInstance, now)
	if e != nil || !next.Allows("assets.hosts") || *next.Quotas.Users != 10 {
		t.Fatal("snapshot mutation leaked into policy", next, e)
	}
	for _, raw := range []string{`{}`, `{"features":{},"quotas":{}}`, `{"features":{},"quotas":{"users":null,"resources":null,"sessions":-1}}`, `{"features":null,"quotas":{"users":null,"resources":null,"sessions":null}}`} {
		if _, e = NewLocal([]byte(raw)); e == nil {
			t.Fatal("invalid local policy accepted")
		}
	}
}

func TestSignedProviderRechecksTimeAndNeverGrantsExpiredFeatures(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	v, _ := NewVerifier(map[string]ed25519.PublicKey{"fixture": pub})
	now := time.Unix(1800000000, 0)
	c := fixtureClaims(now)
	p, e := NewSigned(v, signFixture(key, `{"alg":"EdDSA","kid":"fixture","typ":"nt-license+jws"}`, mustJSON(c)))
	if e != nil {
		t.Fatal(e)
	}
	s, e := p.Snapshot(fixtureInstance, now)
	if e != nil || !s.Allows("assets.hosts") {
		t.Fatal(s, e)
	}
	expired, e := p.Snapshot(fixtureInstance, now.Add(2*time.Hour))
	if !errors.Is(e, ErrExpired) || expired.Allows("assets.hosts") || len(expired.Features) != 0 || expired.ExpiresAt == nil {
		t.Fatal(expired, e)
	}
	invalid, e := p.Snapshot("cf92705e-5818-4370-a6db-e468317b057a", now)
	if !errors.Is(e, ErrInvalid) || invalid.Allows("assets.hosts") {
		t.Fatal(invalid, e)
	}
}
