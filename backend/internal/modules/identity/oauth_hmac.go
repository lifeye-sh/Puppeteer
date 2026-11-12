package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"github.com/google/uuid"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
)

// Independent 32-byte keys authenticate newly issued opaque OAuth capabilities.
// Each row names its key; rotation never requires scanning old keys or changing
// the wire token format. Legacy pre-migration capabilities expire normally.
func (s *Service) EnsureOAuthHMACKey(ctx context.Context, rotate bool) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.EnsureOAuthHMACKey(c, rotate) })
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723046)`); e != nil {
		return e
	}
	var exists bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM oauth_hmac_keys WHERE active)`).Scan(&exists); e != nil {
		return e
	}
	if exists && !rotate {
		return nil
	}
	raw := make([]byte, 32)
	defer clear(raw)
	if _, e := rand.Read(raw); e != nil {
		return e
	}
	return s.ImportOAuthHMACKey(ctx, raw)
}
func (s *Service) ImportOAuthHMACKey(ctx context.Context, raw []byte) error {
	if !s.db.InTransaction(ctx) || len(raw) != 32 {
		return fault.Invalid
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723046)`); e != nil {
		return e
	}
	id := uuid.NewString()
	sealed, e := secretbox.Seal(s.key, raw, "oauth-hmac:"+id)
	if e != nil {
		return e
	}
	if _, e = s.db.Exec(ctx, `UPDATE oauth_hmac_keys SET active=false WHERE active`); e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO oauth_hmac_keys(id,sealed)VALUES($1,$2)`, id, sealed)
	return e
}
func oauthMAC(key []byte, domain, token string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("nt-oauth-v1:" + domain + ":"))
	mac.Write([]byte(token))
	return mac.Sum(nil)
}
func (s *Service) signOAuthOpaque(ctx context.Context, domain, token string) (string, []byte, error) {
	if e := s.EnsureOAuthHMACKey(ctx, false); e != nil {
		return "", nil, e
	}
	var id string
	var sealed []byte
	if e := s.db.Row(ctx, `SELECT id::text,sealed FROM oauth_hmac_keys WHERE active`).Scan(&id, &sealed); e != nil {
		return "", nil, e
	}
	raw, e := secretbox.Open(s.key, sealed, "oauth-hmac:"+id)
	if e != nil {
		return "", nil, e
	}
	defer clear(raw)
	if len(raw) != 32 {
		return "", nil, fault.Proof
	}
	return id, oauthMAC(raw, domain, token), nil
}
func (s *Service) verifyOAuthOpaque(ctx context.Context, id string, tag []byte, domain, token string) error {
	if id == "" && len(tag) == 0 {
		return nil
	} // Only pre-migration rows lack both fields.
	if id == "" || len(tag) != sha256.Size {
		return fault.Proof
	}
	var sealed []byte
	if e := s.db.Row(ctx, `SELECT sealed FROM oauth_hmac_keys WHERE id=$1`, id).Scan(&sealed); e != nil {
		return fault.Proof
	}
	raw, e := secretbox.Open(s.key, sealed, "oauth-hmac:"+id)
	if e != nil {
		return fault.Proof
	}
	defer clear(raw)
	if len(raw) != 32 || !hmac.Equal(tag, oauthMAC(raw, domain, token)) {
		return fault.Proof
	}
	return nil
}
