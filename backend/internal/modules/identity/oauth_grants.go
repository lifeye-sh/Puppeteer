package identity

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
	"slices"
	"time"
)

type OAuthAuthorization struct {
	ClientID    string   `json:"clientId"`
	RedirectURI string   `json:"redirectUri"`
	Scopes      []string `json:"scopes"`
	State       string   `json:"state"`
	Nonce       string   `json:"nonce"`
	Challenge   string   `json:"challenge"`
}
type OAuthPending struct {
	UserID        string
	ClientID      string
	ClientVersion int64
	ServerVersion int64
	AuthEpoch     int64
	Request       OAuthAuthorization
}

func (s *Service) SaveOAuthPending(ctx context.Context, user, session string, c OAuthClient, server OAuthSettings, epoch int64, r OAuthAuthorization) (string, error) {
	token := NewToken()
	data, _ := json.Marshal(r)
	if _, e := s.db.Exec(ctx, `DELETE FROM oauth_pending_authorizations WHERE expires_at<=now() OR session_hash=$1`, Digest(session)); e != nil {
		return "", e
	}
	keyID, tag, err := s.signOAuthOpaque(ctx, "pending", token)
	if err != nil {
		return "", err
	}
	_, e := s.db.Exec(ctx, `INSERT INTO oauth_pending_authorizations(token_hash,session_hash,user_id,client_id,client_version,server_version,auth_epoch,request,expires_at,hmac_key_id,hmac_tag) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()+interval '5 minutes',$9,$10)`, Digest(token), Digest(session), user, c.ID, c.Version, server.Version, epoch, data, keyID, tag)
	return token, e
}
func (s *Service) OAuthPending(ctx context.Context, token, session string, consume bool) (OAuthPending, error) {
	var p OAuthPending
	var macID string
	var tag []byte
	var data []byte
	if len(token) != 64 || len(session) != 64 {
		return p, fault.Proof
	}
	q := `SELECT user_id,client_id,client_version,server_version,auth_epoch,request,coalesce(hmac_key_id::text,''),hmac_tag FROM oauth_pending_authorizations WHERE token_hash=$1 AND session_hash=$2 AND expires_at>now()`
	if consume {
		q = `DELETE FROM oauth_pending_authorizations WHERE token_hash=$1 AND session_hash=$2 AND expires_at>now() RETURNING user_id,client_id,client_version,server_version,auth_epoch,request,coalesce(hmac_key_id::text,''),hmac_tag`
	}
	e := s.db.Row(ctx, q, Digest(token), Digest(session)).Scan(&p.UserID, &p.ClientID, &p.ClientVersion, &p.ServerVersion, &p.AuthEpoch, &data, &macID, &tag)
	if e != nil || s.verifyOAuthOpaque(ctx, macID, tag, "pending", token) != nil || json.Unmarshal(data, &p.Request) != nil {
		return p, fault.Proof
	}
	return p, nil
}

type OAuthGrant struct {
	ID             string
	ClientID       string
	ClientVersion  int64
	ServerVersion  int64
	AuthEpoch      int64
	UserID         string
	UserVersion    int64
	ConsentID      string
	ConsentVersion int64
	Scopes         []string
}

func (s *Service) OAuthCode(ctx context.Context, p OAuthPending, version int64) (string, error) {
	lifetimes, err := s.OAuthLifetimes(ctx, p.ClientID)
	if err != nil {
		return "", err
	}
	var consent string
	var cv int64
	query := `INSERT INTO oauth_consents(client_id,user_id,scopes) VALUES($1,$2,$3) ON CONFLICT(client_id,user_id) DO UPDATE SET scopes=EXCLUDED.scopes,revoked_at=NULL,version=oauth_consents.version+1 RETURNING id,version`
	if p.ClientID == NativeClientUUID {
		// Independent native device logins must not revoke each other. Keep
		// previously approved scopes; each new family still gets only its own
		// requested scopes. Explicit consent revocation invalidates all families.
		query = `INSERT INTO oauth_consents(client_id,user_id,scopes) VALUES($1,$2,$3) ON CONFLICT(client_id,user_id) DO UPDATE SET
		 scopes=CASE WHEN oauth_consents.revoked_at IS NULL THEN ARRAY(SELECT DISTINCT unnest(oauth_consents.scopes||EXCLUDED.scopes)) ELSE EXCLUDED.scopes END,
		 version=CASE WHEN oauth_consents.revoked_at IS NULL THEN oauth_consents.version ELSE oauth_consents.version+1 END,revoked_at=NULL RETURNING id,version`
	}
	e := s.db.Row(ctx, query, p.ClientID, p.UserID, p.Request.Scopes).Scan(&consent, &cv)
	if e != nil {
		return "", e
	}
	token := NewToken()
	keyID, tag, err := s.signOAuthOpaque(ctx, "code", token)
	if err != nil {
		return "", err
	}
	_, e = s.db.Exec(ctx, `INSERT INTO oauth_codes(token_hash,client_id,client_version,server_version,auth_epoch,user_id,user_version,consent_id,consent_version,redirect_uri,challenge,nonce,scopes,expires_at,hmac_key_id,hmac_tag) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now()+$14*interval '1 second',$15,$16)`, Digest(token), p.ClientID, p.ClientVersion, p.ServerVersion, p.AuthEpoch, p.UserID, version, consent, cv, p.Request.RedirectURI, p.Request.Challenge, p.Request.Nonce, p.Request.Scopes, lifetimes.Code, keyID, tag)
	return token, e
}
func (s *Service) ConsumeOAuthCode(ctx context.Context, client, token string) (OAuthGrant, string, string, string, error) {
	var g OAuthGrant
	var redirect, challenge, nonce string
	var macID string
	var tag []byte
	if len(token) != 64 {
		return g, "", "", "", fault.Proof
	}
	e := s.db.Row(ctx, `DELETE FROM oauth_codes WHERE token_hash=$1 AND client_id=$2 AND expires_at>now() RETURNING client_id,client_version,server_version,auth_epoch,user_id,user_version,consent_id,consent_version,redirect_uri,challenge,nonce,scopes,coalesce(hmac_key_id::text,''),hmac_tag`, Digest(token), client).Scan(&g.ClientID, &g.ClientVersion, &g.ServerVersion, &g.AuthEpoch, &g.UserID, &g.UserVersion, &g.ConsentID, &g.ConsentVersion, &redirect, &challenge, &nonce, &g.Scopes, &macID, &tag)
	if e != nil {
		e = fault.Proof
	}
	if e == nil {
		e = s.verifyOAuthOpaque(ctx, macID, tag, "code", token)
	}
	return g, redirect, challenge, nonce, e
}
func (s *Service) ValidateOAuthGrant(ctx context.Context, g OAuthGrant) (OAuthClient, OAuthSettings, error) {
	if g.UserID != "" {
		if e := s.LockUser(ctx, g.UserID); e != nil {
			return OAuthClient{}, OAuthSettings{}, e
		}
	}
	c, e := s.OAuthClient(ctx, g.ClientID, true)
	if e != nil || !c.Enabled || c.Version != g.ClientVersion {
		return c, OAuthSettings{}, fault.Proof
	}
	var server OAuthSettings
	var epoch int64
	e = s.db.Row(ctx, `SELECT o.issuer,o.enabled,o.version,o.native_enabled,i.auth_epoch FROM oauth_server_settings o CROSS JOIN instance_state i WHERE i.initialized_at IS NOT NULL FOR SHARE OF o`).Scan(&server.Issuer, &server.Enabled, &server.Version, &server.NativeEnabled, &epoch)
	if e != nil || !server.AllowsClient(c) || server.Version != g.ServerVersion || epoch != g.AuthEpoch {
		return c, server, fault.Proof
	}
	for _, scope := range g.Scopes {
		if !slices.Contains(c.Scopes, scope) {
			return c, server, fault.Proof
		}
	}
	if g.UserID != "" {
		var ok bool
		e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN oauth_consents o ON o.user_id=u.id WHERE u.id=$1 AND u.version=$2 AND u.status='enabled' AND u.deleted_at IS NULL AND o.id=$3 AND o.client_id=$4 AND o.version=$5 AND o.revoked_at IS NULL)`, g.UserID, g.UserVersion, g.ConsentID, g.ClientID, g.ConsentVersion).Scan(&ok)
		if e != nil || !ok {
			return c, server, fault.Proof
		}
		allowed, e := s.OAuthUserAllowed(ctx, c, g.UserID)
		if e != nil || !allowed {
			return c, server, fault.Proof
		}
	} else if !c.ClientCredentials || len(g.Scopes) != 1 || g.Scopes[0] != "service" {
		return c, server, fault.Proof
	}
	return c, server, nil
}
func (s *Service) CreateOAuthFamily(ctx context.Context, g OAuthGrant) (OAuthGrant, error) {
	lifetimes, err := s.OAuthLifetimes(ctx, g.ClientID)
	if err != nil {
		return g, err
	}
	e := s.db.Row(ctx, `INSERT INTO oauth_token_families(client_id,client_version,server_version,auth_epoch,user_id,user_version,consent_id,consent_version,scopes,expires_at) VALUES($1,$2,$3,$4,nullif($5,'')::uuid,$6,nullif($7,'')::uuid,$8,$9,now()+$10*interval '1 second') RETURNING id`, g.ClientID, g.ClientVersion, g.ServerVersion, g.AuthEpoch, g.UserID, g.UserVersion, g.ConsentID, g.ConsentVersion, g.Scopes, lifetimes.Refresh).Scan(&g.ID)
	if e == nil && g.ClientID == NativeClientUUID && g.UserID != "" {
		_, e = s.db.Exec(ctx, `INSERT INTO native_client_devices(family_id) VALUES($1)`, g.ID)
	}
	return g, e
}
func (s *Service) IssueOAuthTokens(ctx context.Context, g OAuthGrant, refresh bool) (string, string, int, error) {
	lifetimes, err := s.OAuthLifetimes(ctx, g.ClientID)
	if err != nil {
		return "", "", 0, err
	}
	if g.ClientID == NativeClientUUID {
		if _, e := s.db.Exec(ctx, `UPDATE native_client_devices SET last_seen_at=now() WHERE family_id=$1`, g.ID); e != nil {
			return "", "", 0, e
		}
	}
	if _, e := s.db.Exec(ctx, `UPDATE oauth_token_families SET scopes=$2 WHERE id=$1`, g.ID, g.Scopes); e != nil {
		return "", "", 0, e
	}
	access := NewToken()
	keyID, tag, err := s.signOAuthOpaque(ctx, "access", access)
	if err != nil {
		return "", "", 0, err
	}
	var expires int
	if e := s.db.Row(ctx, `INSERT INTO oauth_tokens(token_hash,family_id,kind,expires_at,hmac_key_id,hmac_tag) SELECT $1,id,'access',LEAST(now()+$3*interval '1 second',expires_at),$4,$5 FROM oauth_token_families WHERE id=$2 AND revoked_at IS NULL AND expires_at>now()+interval '1 second' RETURNING floor(extract(epoch FROM (expires_at-now())))::integer`, Digest(access), g.ID, lifetimes.Access, keyID, tag).Scan(&expires); e != nil {
		return "", "", 0, e
	}
	rt := ""
	if refresh {
		rt = NewToken()
		refreshKey, refreshTag, e := s.signOAuthOpaque(ctx, "refresh", rt)
		if e != nil {
			return "", "", 0, e
		}
		if _, e := s.db.Exec(ctx, `INSERT INTO oauth_tokens(token_hash,family_id,kind,expires_at,hmac_key_id,hmac_tag) SELECT $1,id,'refresh',expires_at,$3,$4 FROM oauth_token_families WHERE id=$2`, Digest(rt), g.ID, refreshKey, refreshTag); e != nil {
			return "", "", 0, e
		}
	}
	return access, rt, expires, nil
}

// Caller holds a transaction; a detected refresh replay MUST commit family revocation.
func (s *Service) OAuthToken(ctx context.Context, token, kind, client string, consume bool) (OAuthGrant, bool, error) {
	var g OAuthGrant
	var family, owner string
	var macID string
	var tag []byte
	if len(token) != 64 {
		return g, false, fault.Proof
	}
	if e := s.db.Row(ctx, `SELECT t.family_id,coalesce(f.user_id::text,''),coalesce(t.hmac_key_id::text,''),t.hmac_tag FROM oauth_tokens t JOIN oauth_token_families f ON f.id=t.family_id WHERE t.token_hash=$1 AND t.kind=$2 AND ($3='' OR f.client_id::text=$3)`, Digest(token), kind, client).Scan(&family, &owner, &macID, &tag); e != nil {
		return g, false, fault.Proof
	}
	if e := s.verifyOAuthOpaque(ctx, macID, tag, kind, token); e != nil {
		return g, false, e
	}
	if owner != "" {
		if e := s.LockUser(ctx, owner); e != nil {
			return g, false, e
		}
	}
	e := s.db.Row(ctx, `SELECT id,client_id,client_version,server_version,auth_epoch,coalesce(user_id::text,''),coalesce(user_version,0),coalesce(consent_id::text,''),coalesce(consent_version,0),scopes FROM oauth_token_families WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, family).Scan(&g.ID, &g.ClientID, &g.ClientVersion, &g.ServerVersion, &g.AuthEpoch, &g.UserID, &g.UserVersion, &g.ConsentID, &g.ConsentVersion, &g.Scopes)
	if e != nil {
		return g, false, fault.Proof
	}
	var used *time.Time
	var expires time.Time
	if e = s.db.Row(ctx, `SELECT used_at,expires_at FROM oauth_tokens WHERE token_hash=$1`, Digest(token)).Scan(&used, &expires); e != nil {
		return g, false, e
	}
	if used != nil && consume {
		_, e = s.db.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family)
		return g, true, e
	}
	if !expires.After(time.Now()) {
		return g, false, fault.Proof
	}
	if consume {
		if _, e = s.db.Exec(ctx, `UPDATE oauth_tokens SET used_at=now() WHERE token_hash=$1`, Digest(token)); e != nil {
			return g, false, e
		}
	}
	return g, false, nil
}
func (s *Service) OAuthClaims(ctx context.Context, c OAuthClient, g OAuthGrant) (map[string]any, error) {
	var username, nickname string
	if e := s.db.Row(ctx, `SELECT username,nickname FROM users WHERE id=$1`, g.UserID).Scan(&username, &nickname); e != nil {
		return nil, e
	}
	out := map[string]any{"sub": g.UserID}
	if slices.Contains(g.Scopes, "email") || slices.Contains(g.Scopes, "phone") {
		contacts, e := s.Contacts(ctx, g.UserID)
		if e != nil {
			return nil, e
		}
		for _, c := range contacts {
			if c.Kind == "email" && slices.Contains(g.Scopes, "email") {
				out["email"] = c.Address
				out["email_verified"] = true
			}
			if c.Kind == "phone" && slices.Contains(g.Scopes, "phone") {
				out["phone_number"] = c.Address
				out["phone_number_verified"] = true
			}
		}
	}

	if slices.Contains(g.Scopes, "profile") {
		out["preferred_username"] = username
		out["name"] = nickname
		if c.ClaimMapping["name"] == "username" {
			out["name"] = username
		}
	}
	if slices.Contains(g.Scopes, "groups") {
		rows, e := s.db.Rows(ctx, `SELECT d.id,d.name FROM user_departments u JOIN departments d ON d.id=u.department_id WHERE u.user_id=$1 ORDER BY d.id`, g.UserID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		values := []string{}
		for rows.Next() {
			var id, name string
			if e = rows.Scan(&id, &name); e != nil {
				return nil, e
			}
			if c.ClaimMapping["groups"] == "department_names" {
				values = append(values, name)
			} else {
				values = append(values, id)
			}
		}
		if e = rows.Err(); e != nil {
			return nil, e
		}
		out["groups"] = values
	}
	return out, nil
}
func (s *Service) OAuthConsents(ctx context.Context, user string) ([]map[string]any, error) {
	rows, e := s.db.Rows(ctx, `SELECT o.id,c.name,o.scopes FROM oauth_consents o JOIN oauth_clients c ON c.id=o.client_id WHERE o.user_id=$1 AND o.revoked_at IS NULL ORDER BY c.name`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var scopes []string
		if e = rows.Scan(&id, &name, &scopes); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": id, "name": name, "scopes": scopes})
	}
	return out, rows.Err()
}
func (s *Service) RevokeOAuthConsent(ctx context.Context, user, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE oauth_consents SET revoked_at=now(),version=version+1 WHERE id=$1 AND user_id=$2`, id, user)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return nil
}
func (s *Service) RevokeOAuthToken(ctx context.Context, client, token string) error {
	var keyID, kind string
	var tag []byte
	e := s.db.Row(ctx, `SELECT coalesce(t.hmac_key_id::text,''),t.hmac_tag,t.kind FROM oauth_tokens t JOIN oauth_token_families f ON f.id=t.family_id WHERE f.client_id=$1 AND t.token_hash=$2`, client, Digest(token)).Scan(&keyID, &tag, &kind)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if s.verifyOAuthOpaque(ctx, keyID, tag, kind, token) != nil {
		return nil
	}
	_, e = s.db.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now() WHERE client_id=$1 AND id IN(SELECT family_id FROM oauth_tokens WHERE token_hash=$2)`, client, Digest(token))
	return e
}
