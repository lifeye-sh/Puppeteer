package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
)

// RotationCandidate is an internal encrypted candidate. HTTP DTOs must only
// include Kind/PublicKey; Sealed contains no plaintext but is never exported.
type RotationCandidate struct {
	Kind      string `json:"kind"`
	PublicKey string `json:"publicKey"`
	Sealed    []byte `json:"-"`
}

// NewRotationCandidate does not modify the active version or any asset binding.
// A target-specific AAD prevents swapping encrypted candidates between targets.
func (s *Service) NewRotationCandidate(kind, targetID string) (RotationCandidate, error) {
	var out RotationCandidate
	if targetID == "" {
		return out, fault.Invalid
	}
	in := Write{Credential: Credential{Name: "rotation candidate", Username: "candidate", Kind: kind}}
	switch kind {
	case "password":
		entropy := make([]byte, 32)
		if _, e := rand.Read(entropy); e != nil {
			return out, e
		}
		defer clear(entropy)
		in.Secret = &Secret{Password: base64.RawURLEncoding.EncodeToString(entropy)}
	case "private-key":
		in.Generate = true
	default:
		return out, fault.Invalid
	}
	if e := Validate(&in); e != nil {
		return out, e
	}
	raw, e := json.Marshal(in.Secret)
	if e != nil {
		return out, e
	}
	defer clear(raw)
	out.Kind = kind
	out.PublicKey = in.PublicKey
	out.Sealed, e = secretbox.Seal(s.key, raw, "rotation-target:"+targetID)
	return out, e
}

// OpenRotationCandidate is an internal worker port, never an HTTP reveal route.
// Its caller is responsible for the current lease, authorization and audit.
func (s *Service) OpenRotationCandidate(targetID string, sealed []byte) (Secret, error) {
	var out Secret
	raw, e := secretbox.Open(s.key, sealed, "rotation-target:"+targetID)
	if e != nil {
		return out, e
	}
	defer clear(raw)
	e = json.Unmarshal(raw, &out)
	return out, e
}
