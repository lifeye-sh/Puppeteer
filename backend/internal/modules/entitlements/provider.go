package entitlements

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

type Snapshot struct {
	Source     string          `json:"source"`
	Revision   string          `json:"revision"`
	LicenseID  string          `json:"licenseId,omitempty"`
	InstanceID string          `json:"instanceId"`
	Features   map[string]bool `json:"features"`
	Quotas     Quotas          `json:"quotas"`
	ExpiresAt  *time.Time      `json:"expiresAt,omitempty"`
	State      string          `json:"state"`
	// Local's default grants implemented profiles only; it never invents features.
	AllowImplemented bool `json:"allowImplemented"`
}

func (s Snapshot) Allows(feature string) bool {
	return s.State == "valid" && (s.AllowImplemented || s.Features[feature])
}

type CapabilityProvider interface {
	Snapshot(instanceID string, now time.Time) (Snapshot, error)
}

type localProvider struct {
	config   []byte
	revision string
}
type localConfiguration struct {
	Features map[string]bool `json:"features"`
	Quotas   Quotas          `json:"quotas"`
}

// NewLocal is fed read-only deployment configuration, not an ordinary user's API.
// An empty document preserves all implemented core features and unlimited quotas.
// A nonempty document requires features and quotas explicitly; an empty feature
// object deliberately denies optional capabilities instead of meaning "all".
func NewLocal(raw []byte) (CapabilityProvider, error) {
	if len(raw) > 16384 {
		return nil, ErrInvalid
	}
	if len(raw) == 0 {
		return &localProvider{revision: "local-default-v1"}, nil
	}
	obj, e := object(raw, fields("features", "quotas"))
	if e != nil || len(obj) != 2 {
		return nil, ErrInvalid
	}
	f, e := features(obj["features"])
	if e != nil {
		return nil, e
	}
	q, e := quotas(obj["quotas"])
	if e != nil {
		return nil, e
	}
	canonical, e := json.Marshal(localConfiguration{Features: f, Quotas: q})
	if e != nil {
		return nil, e
	}
	digest := sha256.Sum256(canonical)
	return &localProvider{config: canonical, revision: "local-" + hex.EncodeToString(digest[:])}, nil
}
func (p *localProvider) Snapshot(instanceID string, now time.Time) (Snapshot, error) {
	s := Snapshot{Source: "local", Revision: p.revision, InstanceID: instanceID, State: "valid", Features: map[string]bool{}}
	if len(p.config) == 0 {
		s.AllowImplemented = true
		return s, nil
	}
	var c localConfiguration
	if json.Unmarshal(p.config, &c) != nil {
		return Snapshot{}, ErrInvalid
	}
	s.Features = c.Features
	s.Quotas = c.Quotas
	return s, nil
}

// Signed tokens are loaded by the host's immutable configuration or a future
// audited license repository. Every snapshot verifies signature/time/instance;
// cached verification can never extend a license beyond its validity period.
type signedProvider struct {
	verifier *Verifier
	token    string
	revision string
}

func NewSigned(v *Verifier, token string) (CapabilityProvider, error) {
	if v == nil || len(token) == 0 || len(token) > MaxLicenseBytes {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256([]byte(token))
	return &signedProvider{verifier: v, token: token, revision: "signed-" + hex.EncodeToString(sum[:])}, nil
}
func (p *signedProvider) Snapshot(instanceID string, now time.Time) (Snapshot, error) {
	c, e := p.verifier.Verify(p.token, instanceID, now)
	s := Snapshot{Source: "signed", Revision: p.revision, InstanceID: instanceID, Features: map[string]bool{}, State: "invalid"}
	if e != nil && !errors.Is(e, ErrExpired) && !errors.Is(e, ErrNotYetValid) {
		return s, e
	}
	at := time.Unix(c.ExpiresAt, 0).UTC()
	s.LicenseID = c.LicenseID
	s.ExpiresAt = &at
	s.Quotas = c.Quotas
	switch {
	case errors.Is(e, ErrExpired):
		s.State = "expired"
	case errors.Is(e, ErrNotYetValid):
		s.State = "not_yet_valid"
	default:
		s.State = "valid"
		s.Features = c.Features
	}
	return s, e
}
