package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/go-webauthn/webauthn/webauthn"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

func (s *Service) PasskeyLoginUser(ctx context.Context, rp string, rawID, handle []byte) (PasskeyUser, error) {
	var user string
	if e := s.db.Row(ctx, `SELECT user_id FROM passkey_credentials WHERE rp_id=$1 AND credential_id=$2 AND revoked_at IS NULL`, rp, rawID).Scan(&user); e != nil {
		return PasskeyUser{}, fault.Proof
	}
	if !bytes.Equal([]byte(user), handle) {
		return PasskeyUser{}, fault.Proof
	}
	if e := s.LockUser(ctx, user); e != nil {
		return PasskeyUser{}, e
	}
	var id string
	if e := s.db.Row(ctx, `SELECT id FROM passkey_credentials WHERE rp_id=$1 AND credential_id=$2 AND revoked_at IS NULL FOR UPDATE`, rp, rawID).Scan(&id); e != nil {
		return PasskeyUser{}, fault.Proof
	}
	return s.PasskeyUser(ctx, user, rp)
}
func (s *Service) UpdatePasskeyLogin(ctx context.Context, user, rp string, c *webauthn.Credential) error {
	if c == nil || c.Authenticator.CloneWarning {
		return fault.Proof
	}
	data, e := json.Marshal(c)
	if e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE passkey_credentials SET credential=$4,last_used_at=now() WHERE user_id=$1 AND rp_id=$2 AND credential_id=$3 AND revoked_at IS NULL`, user, rp, c.ID, data)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Proof
	}
	return nil
}

type PasskeyUser struct {
	User
	Credentials []webauthn.Credential
}

func (u PasskeyUser) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u PasskeyUser) WebAuthnName() string                       { return u.Username }
func (u PasskeyUser) WebAuthnDisplayName() string                { return u.Nickname }
func (u PasskeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

type PasskeyRecord struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

func (s *Service) PasskeyUser(ctx context.Context, id, rp string) (PasskeyUser, error) {
	var u PasskeyUser
	e := s.db.Row(ctx, `SELECT id,username,nickname,type FROM users WHERE id=$1 AND status='enabled' AND deleted_at IS NULL AND NOT force_mfa`, id).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type)
	if e != nil {
		return u, e
	}
	rows, e := s.db.Rows(ctx, `SELECT credential FROM passkey_credentials WHERE user_id=$1 AND rp_id=$2 AND revoked_at IS NULL`, id, rp)
	if e != nil {
		return u, e
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var c webauthn.Credential
		if e = rows.Scan(&data); e != nil {
			return u, e
		}
		if e = json.Unmarshal(data, &c); e != nil {
			return u, e
		}
		u.Credentials = append(u.Credentials, c)
	}
	return u, rows.Err()
}
func (s *Service) Passkeys(ctx context.Context, user, rp string) ([]PasskeyRecord, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,created_at,last_used_at,revoked_at FROM passkey_credentials WHERE user_id=$1 AND rp_id=$2 ORDER BY created_at DESC`, user, rp)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PasskeyRecord{}
	for rows.Next() {
		var v PasskeyRecord
		if e = rows.Scan(&v.ID, &v.Name, &v.CreatedAt, &v.LastUsedAt, &v.RevokedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SavePasskey runs under the caller's user lock and audited transaction.
func (s *Service) SavePasskey(ctx context.Context, user, rp, name string, c *webauthn.Credential) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || c == nil || len(c.ID) == 0 {
		return "", fault.Invalid
	}
	var count int
	if e := s.db.Row(ctx, `SELECT count(*) FROM passkey_credentials WHERE user_id=$1 AND revoked_at IS NULL`, user).Scan(&count); e != nil {
		return "", e
	}
	if count >= 20 {
		return "", fault.Conflict
	}
	data, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	var id string
	e = s.db.Row(ctx, `INSERT INTO passkey_credentials(user_id,rp_id,credential_id,name,credential) VALUES($1,$2,$3,$4,$5) RETURNING id`, user, rp, c.ID, name, data).Scan(&id)
	return id, e
}
func (s *Service) SavePasskeyCeremony(ctx context.Context, user, purpose, binding string, session *webauthn.SessionData) (string, error) {
	if binding == "" || session == nil || !session.Expires.After(time.Now()) || (purpose != "register" && purpose != "login") || (purpose == "register" && user == "") {
		return "", fault.Invalid
	}
	data, e := json.Marshal(session)
	if e != nil {
		return "", e
	}
	token := NewToken()
	_, e = s.db.Exec(ctx, `INSERT INTO passkey_ceremonies(token_hash,binding_hash,user_id,purpose,session_data,auth_epoch,expires_at) SELECT $1,$2,nullif($3,'')::uuid,$4,$5,auth_epoch,$6 FROM instance_state WHERE singleton=1`, Digest(token), Digest(binding), user, purpose, data, session.Expires)
	return token, e
}

// ConsumePasskeyCeremony must run outside the verification transaction, so a
// failed signature cannot roll back consumption and reuse the same challenge.
func (s *Service) ConsumePasskeyCeremony(ctx context.Context, token, binding, purpose, user string) (webauthn.SessionData, error) {
	var session webauthn.SessionData
	var data []byte
	if len(token) != 64 || binding == "" {
		return session, fault.Proof
	}
	e := s.db.Row(ctx, `UPDATE passkey_ceremonies c SET consumed_at=now() FROM instance_state i WHERE c.token_hash=$1 AND c.binding_hash=$2 AND c.purpose=$3 AND coalesce(c.user_id::text,'')=$4 AND c.auth_epoch=i.auth_epoch AND c.consumed_at IS NULL AND c.expires_at>now() RETURNING c.session_data`, Digest(token), Digest(binding), purpose, user).Scan(&data)
	if e != nil {
		return session, fault.Proof
	}
	e = json.Unmarshal(data, &session)
	return session, e
}
func (s *Service) RevokePasskey(ctx context.Context, user, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE passkey_credentials SET revoked_at=coalesce(revoked_at,now()) WHERE id=$1 AND user_id=$2`, id, user)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return nil
}
