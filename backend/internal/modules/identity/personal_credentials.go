package identity

import (
	"context"
	"crypto/rsa"
	"errors"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

type PersonalCredential struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix,omitempty"`
	Scopes      []string   `json:"scopes,omitempty"`
	PublicKey   string     `json:"publicKey,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	Algorithm   string     `json:"algorithm,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

func (s *Service) PersonalCredentials(ctx context.Context, user, kind string) ([]PersonalCredential, error) {
	out := []PersonalCredential{}
	query := `SELECT id,name,prefix,scopes,created_at,expires_at,last_used_at,revoked_at FROM personal_api_keys WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`
	if kind == "ssh" {
		query = `SELECT id,name,public_key,fingerprint,algorithm,created_at,expires_at,revoked_at FROM personal_ssh_keys WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`
	}
	rows, e := s.db.Rows(ctx, query, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var v PersonalCredential
		if kind == "ssh" {
			e = rows.Scan(&v.ID, &v.Name, &v.PublicKey, &v.Fingerprint, &v.Algorithm, &v.CreatedAt, &v.ExpiresAt, &v.RevokedAt)
		} else {
			e = rows.Scan(&v.ID, &v.Name, &v.Prefix, &v.Scopes, &v.CreatedAt, &v.ExpiresAt, &v.LastUsedAt, &v.RevokedAt)
		}
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func ValidatePublicKey(input string) (canonical, fingerprint, algorithm string, err error) {
	if len(input) > 16384 {
		return "", "", "", fault.Invalid
	}
	key, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(input))
	if e != nil || len(options) != 0 || strings.TrimSpace(string(rest)) != "" {
		return "", "", "", fault.Invalid
	}
	switch key.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
	case ssh.KeyAlgoRSA:
		crypto, ok := key.(ssh.CryptoPublicKey)
		if !ok {
			return "", "", "", fault.Invalid
		}
		rsaKey, ok := crypto.CryptoPublicKey().(*rsa.PublicKey)
		if !ok || rsaKey.N.BitLen() < 3072 {
			return "", "", "", fault.Invalid
		}
	default:
		return "", "", "", fault.Invalid
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), ssh.FingerprintSHA256(key), key.Type(), nil
}
func (s *Service) CreatePersonalCredential(ctx context.Context, user, kind, name, publicKey string, scopes []string, days int) (id, secret string, err error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 100 || days < 1 || days > 365 {
		return "", "", fault.Invalid
	}
	table := "personal_api_keys"
	if kind == "ssh" {
		table = "personal_ssh_keys"
	}
	var count int
	if err = s.db.Row(ctx, `SELECT count(*) FROM `+table+` WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>now()`, user).Scan(&count); err != nil {
		return
	}
	if count >= 20 {
		return "", "", fault.Conflict
	}
	if kind == "ssh" {
		var canonical, fingerprint, algorithm string
		canonical, fingerprint, algorithm, err = ValidatePublicKey(publicKey)
		if err != nil {
			return
		}
		err = s.db.Row(ctx, `INSERT INTO personal_ssh_keys(user_id,name,public_key,fingerprint,algorithm,expires_at) VALUES($1,$2,$3,$4,$5,now()+$6*interval '1 day') RETURNING id`, user, name, canonical, fingerprint, algorithm, days).Scan(&id)
		return
	}
	if len(scopes) == 0 || len(scopes) > 2 {
		return "", "", fault.Invalid
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if (scope != "profile.read" && scope != "database.read") || seen[scope] {
			return "", "", fault.Invalid
		}
		seen[scope] = true
	}
	secret = "ntk_" + NewToken()
	err = s.db.Row(ctx, `INSERT INTO personal_api_keys(user_id,name,token_hash,prefix,scopes,auth_epoch,expires_at) SELECT $1,$2,$3,$4,$5,auth_epoch,now()+$6*interval '1 day' FROM instance_state WHERE singleton=1 RETURNING id`, user, name, Digest(secret), secret[:12], scopes, days).Scan(&id)
	return
}
func (s *Service) RevokePersonalCredential(ctx context.Context, user, kind, id string) error {
	table := "personal_api_keys"
	if kind == "ssh" {
		table = "personal_ssh_keys"
	}
	result, e := s.db.Exec(ctx, `UPDATE `+table+` SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2`, id, user)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
func (s *Service) CurrentAPIKey(ctx context.Context, secret, scope string) (*User, string, error) {
	if len(secret) != 68 || !strings.HasPrefix(secret, "ntk_") || scope == "" {
		return nil, "", nil
	}
	var user User
	var id string
	e := s.db.Row(ctx, `UPDATE personal_api_keys k SET last_used_at=now() FROM users u,instance_state i WHERE k.user_id=u.id AND k.token_hash=$1 AND $2=ANY(k.scopes) AND k.revoked_at IS NULL AND k.expires_at>now() AND k.auth_epoch=i.auth_epoch AND u.status='enabled' AND NOT u.force_mfa AND u.deleted_at IS NULL RETURNING u.id,u.username,u.nickname,u.type,k.id`, Digest(secret), scope).Scan(&user.ID, &user.Username, &user.Nickname, &user.Type, &id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if e != nil {
		return nil, "", e
	}
	user.Phase = "authenticated"
	return &user, id, nil
}
