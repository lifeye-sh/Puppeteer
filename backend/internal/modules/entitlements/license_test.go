package entitlements

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const fixtureInstance = "71690ba0-40fe-4dbd-adce-f4b7729e2db0"

func fixtureClaims(now time.Time) Claims {
	return Claims{Issuer: Issuer, Audience: Audience, LicenseID: "be369494-6463-4c58-884c-66c084fc4de4", Product: Product, InstanceID: fixtureInstance, IssuedAt: now.Unix() - 10, NotBefore: now.Unix() - 10, ExpiresAt: now.Unix() + 3600, Features: map[string]bool{"assets.hosts": true, "database.postgres": true, "unimplemented.future": true}, SchemaVersion: 1}
}
func signFixture(key ed25519.PrivateKey, header string, payload []byte) string {
	body := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(body)))
}

func TestSignedLicenseStrictVerification(t *testing.T) {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := NewVerifier(map[string]ed25519.PublicKey{"fixture": pub})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Unix(1800000000, 0)
	claims := fixtureClaims(now)
	raw, _ := json.Marshal(claims)
	header := `{"alg":"EdDSA","kid":"fixture","typ":"nt-license+jws"}`
	valid := signFixture(key, header, raw)
	got, e := verifier.Verify(valid, fixtureInstance, now)
	if e != nil || got.LicenseID != claims.LicenseID || got.Quotas.Resources != nil {
		t.Fatal(got, e)
	}
	// The verifier owns a defensive copy of its trust anchors.
	pub[0] ^= 0xff
	if _, e = verifier.Verify(valid, fixtureInstance, now); e != nil {
		t.Fatal("caller mutated verifier key", e)
	}
	cases := map[string]struct {
		header   string
		payload  []byte
		instance string
	}{
		"algorithm confusion":  {strings.Replace(header, "EdDSA", "HS256", 1), raw, fixtureInstance},
		"none":                 {strings.Replace(header, "EdDSA", "none", 1), raw, fixtureInstance},
		"unknown kid":          {strings.Replace(header, "fixture", "other", 1), raw, fixtureInstance},
		"key injection":        {strings.TrimSuffix(header, "}") + `,"jku":"https://attacker.invalid/key"}`, raw, fixtureInstance},
		"duplicate header":     {strings.TrimSuffix(header, "}") + `,"alg":"EdDSA"}`, raw, fixtureInstance},
		"wrong type":           {strings.Replace(header, "nt-license+jws", "JWT", 1), raw, fixtureInstance},
		"duplicate expiration": {header, append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"exp":1800003600}`)...), fixtureInstance},
		"duplicate feature":    {header, bytes.Replace(raw, []byte(`"assets.hosts":true`), []byte(`"assets.hosts":true,"assets.hosts":false`), 1), fixtureInstance},
		"duplicate quota":      {header, bytes.Replace(raw, []byte(`"users":null`), []byte(`"users":null,"users":10`), 1), fixtureInstance},
		"wrong field casing":   {header, bytes.Replace(raw, []byte(`"iss"`), []byte(`"ISS"`), 1), fixtureInstance},
		"wrong issuer":         {header, bytes.Replace(raw, []byte(Issuer), []byte("third-party"), 1), fixtureInstance},
		"wrong instance":       {header, raw, "b93001aa-ae70-4e4e-a1dd-c5e16db37440"},
		"future schema":        {header, bytes.Replace(raw, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":2`), 1), fixtureInstance},
		"negative quota":       {header, bytes.Replace(raw, []byte(`"users":null`), []byte(`"users":-1`), 1), fixtureInstance},
		"fraction quota":       {header, bytes.Replace(raw, []byte(`"users":null`), []byte(`"users":1.5`), 1), fixtureInstance},
		"null features":        {header, bytes.Replace(raw, []byte(`"features":`+string(mustJSON(claims.Features))), []byte(`"features":null`), 1), fixtureInstance},
		"extra claim":          {header, append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"admin":true}`)...), fixtureInstance},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := verifier.Verify(signFixture(key, c.header, c.payload), c.instance, now); !errors.Is(e, ErrInvalid) {
				t.Fatalf("accepted invalid document: %v", e)
			}
		})
	}
	tampered := strings.Split(valid, ".")
	tampered[1] = base64.RawURLEncoding.EncodeToString(bytes.Replace(raw, []byte(`"users":null`), []byte(`"users":100`), 1))
	if _, e = verifier.Verify(strings.Join(tampered, "."), fixtureInstance, now); !errors.Is(e, ErrInvalid) {
		t.Fatal("tampered signature accepted", e)
	}
	for _, token := range []string{"", valid + " ", " " + valid, strings.Repeat("x", MaxLicenseBytes+1), "e30.e30"} {
		if _, e = verifier.Verify(token, fixtureInstance, now); !errors.Is(e, ErrInvalid) {
			t.Fatal("invalid serialization accepted")
		}
	}
}
func mustJSON(value any) []byte {
	raw, e := json.Marshal(value)
	if e != nil {
		panic(e)
	}
	return raw
}

func TestLicenseTimeAndIntersection(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	v, _ := NewVerifier(map[string]ed25519.PublicKey{"fixture": pub})
	now := time.Unix(1800000000, 0)
	c := fixtureClaims(now)
	header := `{"alg":"EdDSA","kid":"fixture","typ":"nt-license+jws"}`
	c.ExpiresAt = now.Unix() - 61
	c.IssuedAt = now.Unix() - 300
	c.NotBefore = c.IssuedAt
	if got, e := v.Verify(signFixture(key, header, mustJSON(c)), fixtureInstance, now); !errors.Is(e, ErrExpired) || got.LicenseID == "" {
		t.Fatal(got, e)
	}
	c = fixtureClaims(now)
	c.NotBefore = now.Unix() + 61
	if _, e := v.Verify(signFixture(key, header, mustJSON(c)), fixtureInstance, now); !errors.Is(e, ErrNotYetValid) {
		t.Fatal(e)
	}
	c = fixtureClaims(now)
	c.ExpiresAt = 253402300800
	if _, e := v.Verify(signFixture(key, header, mustJSON(c)), fixtureInstance, now); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	for mask := 0; mask < 16; mask++ {
		got := Intersect(mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0, nil)
		if got.Available != (mask == 15) {
			t.Fatal(mask, got)
		}
	}
	if got := Intersect(false, true, true, true, nil); got.DisabledReason != "not_implemented" || got.Available {
		t.Fatal(got)
	}
	if got := Intersect(true, true, true, true, ErrExpired); got.DisabledReason != "expired" || got.Available {
		t.Fatal(got)
	}
	if got := Intersect(true, true, true, true, ErrInvalid); got.DisabledReason != "invalid_license" || got.Available {
		t.Fatal(got)
	}
}
