package identity

import (
	"context"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
)

const NativeClientID = "nt-native-v1"
const NativeClientUUID = "00000000-0000-4000-8000-000000000014"

func (c OAuthClient) Native() bool { return c.ID == NativeClientUUID }
func (s OAuthSettings) AllowsClient(c OAuthClient) bool {
	if s.Issuer == "" {
		return false
	}
	if c.Native() {
		return s.NativeEnabled
	}
	return s.Enabled
}

// Only this built-in public client permits an HTTP loopback callback. Nothing
// except the ephemeral TCP port may vary; DNS names and encoded paths fail.
func NativeRedirect(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 200 || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/callback" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "#\\\r\n") {
		return false
	}
	port, e := strconv.Atoi(u.Port())
	return e == nil && port >= 1024 && port <= 65535 && raw == "http://127.0.0.1:"+strconv.Itoa(port)+"/callback"
}

// Native bearer tokens are deliberately separate from browser/API-key auth.
// Caller holds a transaction so token, user and grant checks are consistent.
func (s *Service) NativeUser(ctx context.Context, token, scope string) (*User, error) {
	g, _, e := s.OAuthToken(ctx, token, "access", NativeClientUUID, false)
	if e != nil {
		return nil, fault.Proof
	}
	return s.nativeGrantUser(ctx, g, scope)
}
func (s *Service) nativeGrantUser(ctx context.Context, g OAuthGrant, scope string) (*User, error) {
	if g.ClientID != NativeClientUUID {
		return nil, fault.Proof
	}
	var e error
	if _, _, e = s.ValidateOAuthGrant(ctx, g); e != nil {
		return nil, fault.Proof
	}
	found := false
	for _, v := range g.Scopes {
		if v == scope {
			found = true
		}
	}
	if !found || g.UserID == "" {
		return nil, fault.Denied
	}
	u := &User{Phase: "authenticated"}
	e = s.db.Row(ctx, `SELECT id,username,nickname,type FROM users WHERE id=$1 AND status='enabled' AND deleted_at IS NULL`, g.UserID).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type)
	if e == nil {
		_, e = s.db.Exec(ctx, `UPDATE native_client_devices SET last_seen_at=now() WHERE family_id=$1 AND (last_seen_at IS NULL OR last_seen_at<now()-interval '1 minute')`, g.ID)
	}
	return u, e
}

// Existing connections follow the authorization family, not a five-minute access
// token. New connections still require an unexpired access token. Family expiry,
// revocation, consent/client/user/server versions and the auth epoch remain live.
// Caller must hold a transaction; lock ordering matches OAuthToken.
func (s *Service) NativeFamilyUser(ctx context.Context, family string) (*User, error) {
	var owner string
	if e := s.db.Row(ctx, `SELECT user_id FROM oauth_token_families WHERE id=$1 AND client_id=$2`, family, NativeClientUUID).Scan(&owner); e != nil {
		return nil, fault.Proof
	}
	if e := s.LockUser(ctx, owner); e != nil {
		return nil, e
	}
	var g OAuthGrant
	e := s.db.Row(ctx, `SELECT id,client_id,client_version,server_version,auth_epoch,user_id,user_version,consent_id,consent_version,scopes FROM oauth_token_families WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() FOR SHARE`, family).Scan(&g.ID, &g.ClientID, &g.ClientVersion, &g.ServerVersion, &g.AuthEpoch, &g.UserID, &g.UserVersion, &g.ConsentID, &g.ConsentVersion, &g.Scopes)
	if e != nil {
		return nil, fault.Proof
	}
	return s.nativeGrantUser(ctx, g, "sessions:connect")
}
