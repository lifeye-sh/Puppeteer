// Package entitlements verifies this project's own licenses. It does not accept
// or emulate licenses issued by upstream or any third-party vendor.
package entitlements

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
)

const Product = "next-terminal-backend"
const Issuer = "next-terminal-local-issuer"
const Audience = "next-terminal-backend"
const MaxLicenseBytes = 32768

var ErrInvalid = errors.New("invalid entitlement document")
var ErrExpired = errors.New("entitlement expired")
var ErrNotYetValid = errors.New("entitlement not yet valid")

type Quotas struct {
	Users     *int64 `json:"users"`
	Resources *int64 `json:"resources"`
	Sessions  *int64 `json:"sessions"`
}
type Claims struct {
	Issuer        string          `json:"iss"`
	Audience      string          `json:"aud"`
	LicenseID     string          `json:"licenseId"`
	Product       string          `json:"product"`
	InstanceID    string          `json:"instanceId"`
	IssuedAt      int64           `json:"iat"`
	NotBefore     int64           `json:"nbf"`
	ExpiresAt     int64           `json:"exp"`
	Features      map[string]bool `json:"features"`
	Quotas        Quotas          `json:"quotas"`
	SchemaVersion int             `json:"schemaVersion"`
}

// Verifier's keys come from a read-only deployment trust store, never from a
// license's jwk/jku/x5u fields. No signing private key is required by the server.
type Verifier struct{ keys map[string]ed25519.PublicKey }

func NewVerifier(keys map[string]ed25519.PublicKey) (*Verifier, error) {
	if len(keys) == 0 || len(keys) > 16 {
		return nil, ErrInvalid
	}
	v := &Verifier{keys: map[string]ed25519.PublicKey{}}
	for id, key := range keys {
		if !keyID.MatchString(id) || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalid
		}
		v.keys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return v, nil
}

var keyID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var featureID = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_-]*){1,4}$`)

// object rejects duplicate members, wrong casing, unexpected fields and trailing
// input. Nested feature/quotas objects are validated separately below.
func object(raw []byte, allowed map[string]bool) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, ErrInvalid
	}
	result := map[string]json.RawMessage{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return nil, ErrInvalid
		}
		name, ok := key.(string)
		if !ok {
			return nil, ErrInvalid
		}
		if _, ok = result[name]; ok {
			return nil, ErrInvalid
		}
		if allowed != nil && !allowed[name] {
			return nil, ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, ErrInvalid
		}
		result[name] = value
	}
	if token, e = d.Token(); e != nil || token != json.Delim('}') {
		return nil, ErrInvalid
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, ErrInvalid
	}
	return result, nil
}
func fields(names ...string) map[string]bool {
	v := map[string]bool{}
	for _, name := range names {
		v[name] = true
	}
	return v
}
func quotas(raw []byte) (Quotas, error) {
	allowed := fields("users", "resources", "sessions")
	obj, e := object(raw, allowed)
	if e != nil || len(obj) != len(allowed) {
		return Quotas{}, ErrInvalid
	}
	var q Quotas
	if json.Unmarshal(raw, &q) != nil {
		return q, ErrInvalid
	}
	for _, n := range []*int64{q.Users, q.Resources, q.Sessions} {
		if n != nil && (*n < 0 || *n > 1000000000) {
			return Quotas{}, ErrInvalid
		}
	}
	return q, nil
}
func features(raw []byte) (map[string]bool, error) {
	obj, e := object(raw, nil)
	if e != nil || len(obj) > 256 {
		return nil, ErrInvalid
	}
	v := map[string]bool{}
	for key, raw := range obj {
		if !featureID.MatchString(key) {
			return nil, ErrInvalid
		}
		var enabled bool
		if string(raw) == "null" || json.Unmarshal(raw, &enabled) != nil {
			return nil, ErrInvalid
		}
		v[key] = enabled
	}
	return v, nil
}

// Verify accepts only compact EdDSA JWS with all policy claims present. Claims
// returned alongside ErrExpired/ErrNotYetValid are authenticated metadata only,
// and MUST NOT grant access. Clock tolerance is 60 seconds; no offline grace.
func (v *Verifier) Verify(token, instance string, now time.Time) (Claims, error) {
	var zero Claims
	if v == nil || len(token) == 0 || len(token) > MaxLicenseBytes || strings.TrimSpace(token) != token {
		return zero, ErrInvalid
	}
	if id, e := uuid.Parse(instance); e != nil || id.String() != instance {
		return zero, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return zero, ErrInvalid
	}
	header, e := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if e != nil || len(header) > 1024 {
		return zero, ErrInvalid
	}
	obj, e := object(header, fields("alg", "kid", "typ"))
	if e != nil || len(obj) != 3 {
		return zero, ErrInvalid
	}
	var h struct {
		Alg   string `json:"alg"`
		KeyID string `json:"kid"`
		Type  string `json:"typ"`
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "EdDSA" || h.Type != "nt-license+jws" {
		return zero, ErrInvalid
	}
	key, ok := v.keys[h.KeyID]
	if !ok {
		return zero, ErrInvalid
	}
	signed, e := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if e != nil || len(signed.Signatures) != 1 {
		return zero, ErrInvalid
	}
	raw, e := signed.Verify(key)
	if e != nil || len(raw) > 16384 {
		return zero, ErrInvalid
	}
	allowed := fields("iss", "aud", "licenseId", "product", "instanceId", "iat", "nbf", "exp", "features", "quotas", "schemaVersion")
	values, e := object(raw, allowed)
	if e != nil || len(values) != len(allowed) {
		return zero, ErrInvalid
	}
	var c Claims
	if json.Unmarshal(raw, &c) != nil {
		return zero, ErrInvalid
	}
	c.Features, e = features(values["features"])
	if e != nil {
		return zero, e
	}
	c.Quotas, e = quotas(values["quotas"])
	if e != nil {
		return zero, e
	}
	if c.Issuer != Issuer || c.Audience != Audience || c.Product != Product || c.InstanceID != instance || c.SchemaVersion != 1 {
		return zero, ErrInvalid
	}
	if id, e := uuid.Parse(c.LicenseID); e != nil || id.String() != c.LicenseID {
		return zero, ErrInvalid
	}
	if c.IssuedAt < 1 || c.NotBefore < 1 || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt <= c.NotBefore {
		return zero, ErrInvalid
	}
	// Bound Unix seconds before arithmetic and time conversion.
	if c.IssuedAt > 253402300799 || c.NotBefore > 253402300799 || c.ExpiresAt > 253402300799 {
		return zero, ErrInvalid
	}
	seconds := now.Unix()
	if c.IssuedAt > seconds+60 || c.NotBefore > seconds+60 {
		return c, ErrNotYetValid
	}
	if c.ExpiresAt <= seconds-60 {
		return c, ErrExpired
	}
	return c, nil
}

type Capability struct {
	Implemented    bool   `json:"implemented"`
	Available      bool   `json:"available"`
	DisabledReason string `json:"disabledReason,omitempty"`
}

// Intersect cannot enable a capability absent from the implementation/profile.
func Intersect(implemented, configured, tested, entitled bool, licenseErr error) Capability {
	v := Capability{Implemented: implemented}
	switch {
	case !implemented:
		v.DisabledReason = "not_implemented"
	case errors.Is(licenseErr, ErrExpired):
		v.DisabledReason = "expired"
	case licenseErr != nil:
		v.DisabledReason = "invalid_license"
	case !entitled:
		v.DisabledReason = "not_entitled"
	case !configured:
		v.DisabledReason = "not_configured"
	case !tested:
		v.DisabledReason = "profile_not_tested"
	default:
		v.Available = true
	}
	return v
}
