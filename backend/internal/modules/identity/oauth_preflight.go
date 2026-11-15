package identity

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"

	"puppeteer/backend/internal/platform/fault"
	"puppeteer/backend/internal/platform/secretbox"
)

// OAuthPreflightKey reads and validates the active signing and HMAC material.
// It deliberately does not call EnsureOAuthKey/EnsureOAuthHMACKey.
func (s *Service) OAuthPreflightKey(ctx context.Context) (json.RawMessage, error) {
	var id string
	var sealed, public []byte
	if err := s.db.Row(ctx, `SELECT id,private_sealed,public_jwk FROM oauth_signing_keys WHERE active`).Scan(&id, &sealed, &public); err != nil {
		return nil, err
	}
	raw, err := secretbox.Open(s.key, sealed, "oauth-signing:"+id)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	key, err := x509.ParsePKCS1PrivateKey(raw)
	if err != nil || key.Validate() != nil {
		return nil, fault.Invalid
	}
	var jwk struct{ Kid, N, E, Kty, Alg string }
	if json.Unmarshal(public, &jwk) != nil || jwk.Kid != id || jwk.Kty != "RSA" || jwk.Alg != "RS256" || jwk.N != base64.RawURLEncoding.EncodeToString(key.N.Bytes()) || jwk.E != base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()) {
		return nil, fault.Invalid
	}
	if err = s.db.Row(ctx, `SELECT id::text,sealed FROM oauth_hmac_keys WHERE active`).Scan(&id, &sealed); err != nil {
		return nil, err
	}
	hmac, err := secretbox.Open(s.key, sealed, "oauth-hmac:"+id)
	defer clear(hmac)
	if err != nil || len(hmac) != 32 {
		return nil, fault.Invalid
	}
	return public, nil
}
