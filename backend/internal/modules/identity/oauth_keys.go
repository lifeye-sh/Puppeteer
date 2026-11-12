package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"math/big"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/oauthkeys"
	"next-terminal/backend/internal/platform/secretbox"
	"time"
)

func (s *Service) EnsureOAuthKey(ctx context.Context, rotate bool) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.EnsureOAuthKey(c, rotate) })
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723045)`); e != nil {
		return e
	}
	var exists bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM oauth_signing_keys WHERE active)`).Scan(&exists); e != nil {
		return e
	}
	if exists && !rotate {
		return nil
	}
	key, e := rsa.GenerateKey(rand.Reader, 3072)
	if e != nil {
		return e
	}
	return s.installOAuthSigningKey(ctx, key)
}

// Import is called only inside the password/MFA configuration transaction.
func (s *Service) ImportOAuthSigningKey(ctx context.Context, raw []byte) error {
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	key, e := oauthkeys.Signing(raw)
	if e != nil {
		return fault.Invalid
	}
	if _, e = s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723045)`); e != nil {
		return e
	}
	return s.installOAuthSigningKey(ctx, key)
}
func (s *Service) installOAuthSigningKey(ctx context.Context, key *rsa.PrivateKey) error {
	id := NewToken()
	raw := x509.MarshalPKCS1PrivateKey(key)
	defer clear(raw)
	sealed, e := secretbox.Seal(s.key, raw, "oauth-signing:"+id)
	if e != nil {
		return e
	}
	jwk, _ := json.Marshal(map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": id, "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())})
	// Preserve verifiability for the maximum configurable ID-token lifetime,
	// including a minute for in-flight signing and clock tolerance.
	if _, e = s.db.Exec(ctx, `UPDATE oauth_signing_keys SET active=false,retire_at=now()+interval '3660 seconds' WHERE active`); e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO oauth_signing_keys(id,private_sealed,public_jwk) VALUES($1,$2,$3)`, id, sealed, jwk)
	return e
}
func (s *Service) OAuthJWKS(ctx context.Context) ([]json.RawMessage, error) {
	rows, e := s.db.Rows(ctx, `SELECT public_jwk FROM oauth_signing_keys WHERE active OR retire_at>now() ORDER BY created_at`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}
func (s *Service) SignOAuthIDToken(ctx context.Context, issuer, client, nonce string, claims map[string]any) (string, error) {
	lifetimes, err := s.OAuthLifetimes(ctx, client)
	if err != nil {
		return "", err
	}
	var id string
	var sealed []byte
	if e := s.db.Row(ctx, `SELECT id,private_sealed FROM oauth_signing_keys WHERE active`).Scan(&id, &sealed); e != nil {
		return "", e
	}
	raw, e := secretbox.Open(s.key, sealed, "oauth-signing:"+id)
	if e != nil {
		return "", e
	}
	defer clear(raw)
	key, e := x509.ParsePKCS1PrivateKey(raw)
	if e != nil {
		return "", e
	}
	values := jwt.MapClaims{}
	for k, v := range claims {
		values[k] = v
	}
	values["iss"] = issuer
	values["aud"] = client
	issued := time.Now()
	values["iat"] = issued.Unix()
	values["exp"] = issued.Add(time.Duration(lifetimes.IDToken) * time.Second).Unix()
	values["jti"] = NewToken()
	if nonce != "" {
		values["nonce"] = nonce
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, values)
	token.Header["kid"] = id
	return token.SignedString(key)
}
