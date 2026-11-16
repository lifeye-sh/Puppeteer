package systemupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(now time.Time) Catalog {
	r := Release{Version: "beta0.11", Channel: "beta", PublishedAt: now, Platform: "linux/amd64", MinSource: "beta0.9", SchemaVersion: 110, CapabilitiesRevision: 90, NotesURL: "https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.11"}
	for _, name := range []string{"server", "web", "graphics", "renderer"} {
		d := "sha256:" + strings.Repeat("a", 64)
		r.Images = append(r.Images, Image{name, d, "cloners/puppeteer-" + name + "@" + d})
	}
	return Catalog{Product: "Puppeteer", Format: 1, Sequence: 2, IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour), Releases: []Release{r}}
}
func signed(t *testing.T, c Catalog, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(Envelope{"test", base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestVersionOrdering(t *testing.T) {
	for _, pair := range [][2]string{{"r17-rc13", "beta0.9"}, {"beta0.9", "beta0.10"}, {"beta0.10", "beta0.11"}, {"beta1.99", "beta2.0"}, {"beta2.0", "v2.0.0"}, {"v2.9.0", "v2.10.0"}} {
		if n, e := Compare(pair[0], pair[1]); e != nil || n != -1 {
			t.Fatal(pair, n, e)
		}
	}
	for _, v := range []string{"beta01.9", "beta0.09", "beta0.10\n", "latest", "beta0.10;id", "v1.0.0/x", "beta999999999999.1"} {
		if _, e := Compare(v, "beta0.10"); e == nil {
			t.Fatal("accepted", v)
		}
	}
}
func TestCatalogAuthenticityAndRollback(t *testing.T) {
	now := time.Now().UTC()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	keys := map[string]ed25519.PublicKey{"test": pub}
	c := fixture(now)
	raw := signed(t, c, key)
	_, digest, e := Verify(raw, keys, now, 0, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(raw, keys, now, 2, digest); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(raw, keys, now, 3, digest); e == nil {
		t.Fatal("rollback accepted")
	}
	if _, _, e = Verify(raw, keys, now, 2, "different"); e == nil {
		t.Fatal("equivocation accepted")
	}
	if _, _, e = Verify(raw, map[string]ed25519.PublicKey{}, now, 0, ""); e == nil {
		t.Fatal("unknown signer accepted")
	}
	var env Envelope
	json.Unmarshal(raw, &env)
	env.Payload = base64.StdEncoding.EncodeToString([]byte(`{}`))
	bad, _ := json.Marshal(env)
	if _, _, e = Verify(bad, keys, now, 0, ""); e == nil {
		t.Fatal("tampering accepted")
	}
	for _, change := range []func(*Catalog){
		func(c *Catalog) { c.ExpiresAt = now.Add(-time.Second) }, func(c *Catalog) { c.IssuedAt = now.Add(time.Hour) },
		func(c *Catalog) { c.Releases[0].Images[0].PinnedReference = "evil/runner:latest" },
		func(c *Catalog) { c.Releases[0].MinSource = "beta0.12" }, func(c *Catalog) { c.Releases[0].NotesURL = "javascript:alert(1)" },
		func(c *Catalog) { c.Releases[0].Images[1] = c.Releases[0].Images[0] }, func(c *Catalog) { c.Releases = append(c.Releases, c.Releases[0]) },
	} {
		copy := fixture(now)
		change(&copy)
		if _, _, e = Verify(signed(t, copy, key), keys, now, 0, ""); e == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
	if _, _, e = Verify(append(raw, []byte(`{}`)...), keys, now, 0, ""); e == nil {
		t.Fatal("extra JSON accepted")
	}
}
func TestChecksPersistAndFailuresNeverReportLatest(t *testing.T) {
	now := time.Now().UTC()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	keys := map[string]ed25519.PublicKey{"test": pub}
	raw := signed(t, fixture(now), key)
	fail := false
	fetch := func(context.Context) ([]byte, error) {
		if fail {
			return nil, errors.New("fixture offline")
		}
		return raw, nil
	}
	path := filepath.Join(t.TempDir(), "runtime", "updates.json")
	m, e := New(path, keys, fetch)
	if e != nil {
		t.Fatal(e)
	}
	m.now = func() time.Time { return now }
	if !m.Due() {
		t.Fatal("initial check not due")
	}
	if e = m.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
	if m.View("beta0.10").State != "update_available" || m.View("beta0.11").State != "up_to_date" || m.View("custom").State != "custom_build" {
		t.Fatal("version states")
	}
	if m.Due() {
		t.Fatal("schedule storm")
	}
	if e = m.Check(context.Background()); e == nil {
		t.Fatal("manual flood")
	}
	fresh, e := New(path, keys, fetch)
	if e != nil || fresh.View("beta0.10").State != "update_available" {
		t.Fatal("restart", e)
	}
	if _, e = fresh.Target("beta0.11"); e != nil {
		t.Fatal(e)
	}
	s := m.View("beta0.10").Settings
	s.Channel = "stable"
	if e = m.Configure(s); e != nil {
		t.Fatal(e)
	}
	if e = m.Configure(s); e == nil {
		t.Fatal("stale settings accepted")
	}
	if m.View("beta0.10").State != "no_channel_release" {
		t.Fatal("empty channel is not latest")
	}
	now = now.Add(time.Minute)
	fail = true
	if e = m.Check(context.Background()); e == nil {
		t.Fatal("offline check succeeded")
	}
	v := m.View("beta0.11")
	if v.State != "check_failed" || v.LastSuccess.IsZero() || len(v.History) != 2 {
		t.Fatal(v)
	}
	if _, e = m.Target("beta0.11"); e == nil {
		t.Fatal("stale target accepted")
	}
	// A cache is verified against embedded trust again after process restart.
	cached, _ := os.ReadFile(path)
	var state State
	json.Unmarshal(cached, &state)
	state.Envelope = []byte(`{"keyId":"evil","payload":"","signature":""}`)
	b, _ := json.Marshal(state)
	os.WriteFile(path, b, 0600)
	fresh, e = New(path, keys, fetch)
	if e != nil || fresh.View("beta0.11").State != "check_failed" {
		t.Fatal("cache trust", e)
	}
}
func TestCheckConcurrencyAndCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m, _ := New("", nil, func(ctx context.Context) ([]byte, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, errors.New("cancelled")
	})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- m.Check(ctx) }()
	<-entered
	if e := m.Check(context.Background()); e == nil {
		t.Fatal("concurrent check accepted")
	}
	cancel()
	if <-done == nil {
		t.Fatal("cancelled check succeeded")
	}
	if m.View("beta0.10").State != "check_failed" {
		t.Fatal("cancelled check state")
	}
}
