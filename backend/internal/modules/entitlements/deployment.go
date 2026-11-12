package entitlements

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
)

// Invalid signed deployments never fall back to unlimited local mode.
func Deployment(mode string, local, trust []byte) (CapabilityProvider, *Verifier, error) {
	switch mode {
	case "", "local":
		if len(trust) != 0 {
			return nil, nil, ErrInvalid
		}
		p, e := NewLocal(local)
		return p, nil, e
	case "signed":
		if len(local) != 0 || len(trust) > 16384 {
			return nil, nil, ErrInvalid
		}
		obj, e := object(trust, fields("keys"))
		if e != nil || len(obj) != 1 {
			return nil, nil, ErrInvalid
		}
		keys, e := object(obj["keys"], nil)
		if e != nil {
			return nil, nil, ErrInvalid
		}
		decoded := map[string]ed25519.PublicKey{}
		for id, raw := range keys {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return nil, nil, ErrInvalid
			}
			key, e := base64.StdEncoding.Strict().DecodeString(value)
			if e != nil {
				return nil, nil, ErrInvalid
			}
			decoded[id] = ed25519.PublicKey(key)
		}
		v, e := NewVerifier(decoded)
		return nil, v, e
	default:
		return nil, nil, ErrInvalid
	}
}
