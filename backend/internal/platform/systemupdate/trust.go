package systemupdate

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
)

// Trust anchors ship with the application, never with the downloaded index.
//
//go:embed trust.json
var trust []byte

func TrustedKeys() map[string]ed25519.PublicKey {
	var values map[string]string
	out := map[string]ed25519.PublicKey{}
	if json.Unmarshal(trust, &values) != nil {
		return out
	}
	for id, value := range values {
		b, e := base64.StdEncoding.Strict().DecodeString(value)
		if e == nil && len(b) == ed25519.PublicKeySize {
			out[id] = ed25519.PublicKey(b)
		}
	}
	return out
}
