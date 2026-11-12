package identity

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/url"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"slices"
	"strings"
)

type OAuthSettings struct {
	NativeEnabled bool   `json:"nativeEnabled"`
	Issuer        string `json:"issuer"`
	Enabled       bool   `json:"enabled"`
	Version       int64  `json:"version"`
}

func (s *Service) OAuthSettings(ctx context.Context) (OAuthSettings, error) {
	var v OAuthSettings
	e := s.db.Row(ctx, `SELECT issuer,enabled,version,native_enabled FROM oauth_server_settings WHERE singleton=1`).Scan(&v.Issuer, &v.Enabled, &v.Version, &v.NativeEnabled)
	return v, e
}

// SaveOAuthSettings is the legacy editor adapter; the caller must provide the
// same administrator proof context as the unified settings API.
func (s *Service) SaveOAuthSettings(ctx context.Context, v OAuthSettings, actor string) error {
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	var revision, version int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&revision); e != nil {
		return e
	}
	if e := s.db.Row(ctx, `SELECT version FROM oauth_server_settings WHERE singleton=1`).Scan(&version); e != nil {
		return e
	}
	if version != v.Version {
		return fault.Conflict
	}
	patch := map[string]json.RawMessage{}
	for k, value := range map[string]any{system.OAuthServerEnabledKey: v.Enabled, system.OAuthServerIssuerKey: v.Issuer, system.NativeClientEnabledKey: v.NativeEnabled} {
		patch[k], _ = json.Marshal(value)
	}
	_, e := system.New(s.db).SaveConfiguration(ctx, &revision, patch, actor)
	return e
}

type OAuthClient struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	RedirectURIs       []string          `json:"redirectUris"`
	Scopes             []string          `json:"scopes"`
	AllowedUsers       []string          `json:"allowedUsers"`
	AllowedDepartments []string          `json:"allowedDepartments"`
	AllowAllUsers      bool              `json:"allowAllUsers"`
	ClientCredentials  bool              `json:"clientCredentials"`
	Enabled            bool              `json:"enabled"`
	ClaimMapping       map[string]string `json:"claimMapping"`
	Version            int64             `json:"version"`
}

const oauthClientFields = `id,name,redirect_uris,scopes,allowed_users,allowed_departments,allow_all_users,client_credentials,enabled,claim_mapping,version`

func (s *Service) OAuthClient(ctx context.Context, id string, lock bool) (OAuthClient, error) {
	var v OAuthClient
	q := `SELECT ` + oauthClientFields + ` FROM oauth_clients WHERE id=$1`
	if lock {
		q += " FOR SHARE"
	}
	e := s.db.Row(ctx, q, id).Scan(&v.ID, &v.Name, &v.RedirectURIs, &v.Scopes, &v.AllowedUsers, &v.AllowedDepartments, &v.AllowAllUsers, &v.ClientCredentials, &v.Enabled, &v.ClaimMapping, &v.Version)
	return v, e
}
func (s *Service) OAuthClients(ctx context.Context) ([]OAuthClient, error) {
	rows, e := s.db.Rows(ctx, `SELECT `+oauthClientFields+` FROM oauth_clients WHERE id<>'00000000-0000-4000-8000-000000000014' ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OAuthClient{}
	for rows.Next() {
		var v OAuthClient
		if e = rows.Scan(&v.ID, &v.Name, &v.RedirectURIs, &v.Scopes, &v.AllowedUsers, &v.AllowedDepartments, &v.AllowAllUsers, &v.ClientCredentials, &v.Enabled, &v.ClaimMapping, &v.Version); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveOAuthClient(ctx context.Context, v OAuthClient, rotate bool) (string, string, error) {
	if v.ID == NativeClientUUID {
		return "", "", fault.Denied
	}
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 || len(v.RedirectURIs) > 20 || len(v.Scopes) == 0 || len(v.Scopes) > 7 || len(v.AllowedUsers) > 1000 || len(v.AllowedDepartments) > 1000 || len(v.ClaimMapping) > 3 {
		return "", "", fault.Invalid
	}
	if v.AllowedUsers == nil {
		v.AllowedUsers = []string{}
	}
	if v.AllowedDepartments == nil {
		v.AllowedDepartments = []string{}
	}
	if v.RedirectURIs == nil {
		v.RedirectURIs = []string{}
	}
	if v.ClaimMapping == nil {
		v.ClaimMapping = map[string]string{}
	}
	for _, value := range v.RedirectURIs {
		u, e := url.Parse(value)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(value) > 2048 || strings.ContainsAny(value, "*\r\n\\#") {
			return "", "", fault.Invalid
		}
	}
	for _, scope := range v.Scopes {
		if !slices.Contains([]string{"openid", "profile", "email", "phone", "groups", "offline_access", "service"}, scope) {
			return "", "", fault.Invalid
		}
	}
	if len(v.RedirectURIs) == 0 && !v.ClientCredentials {
		return "", "", fault.Invalid
	}
	for _, id := range append(append([]string{}, v.AllowedUsers...), v.AllowedDepartments...) {
		if _, e := uuid.Parse(id); e != nil {
			return "", "", fault.Invalid
		}
	}
	for k, value := range v.ClaimMapping {
		if !(k == "name" && slices.Contains([]string{"nickname", "username"}, value) || k == "preferred_username" && value == "username" || k == "groups" && slices.Contains([]string{"department_ids", "department_names"}, value)) {
			return "", "", fault.Invalid
		}
	}
	data, _ := json.Marshal(v.ClaimMapping)
	secret := ""
	if v.ID == "" || rotate {
		secret = NewToken()
	}
	if v.ID == "" {
		e := s.db.Row(ctx, `INSERT INTO oauth_clients(name,secret_hash,redirect_uris,scopes,allowed_users,allowed_departments,allow_all_users,client_credentials,enabled,claim_mapping) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, v.Name, Digest(secret), v.RedirectURIs, v.Scopes, v.AllowedUsers, v.AllowedDepartments, v.AllowAllUsers, v.ClientCredentials, v.Enabled, data).Scan(&v.ID)
		if e != nil {
			return "", "", e
		}
	} else {
		tag, e := s.db.Exec(ctx, `UPDATE oauth_clients SET name=$2,redirect_uris=$3,scopes=$4,allowed_users=$5,allowed_departments=$6,allow_all_users=$7,client_credentials=$8,enabled=$9,claim_mapping=$10,version=version+1,secret_hash=CASE WHEN $12='' THEN secret_hash ELSE $12 END WHERE id=$1 AND version=$11`, v.ID, v.Name, v.RedirectURIs, v.Scopes, v.AllowedUsers, v.AllowedDepartments, v.AllowAllUsers, v.ClientCredentials, v.Enabled, data, v.Version, func() string {
			if secret == "" {
				return ""
			}
			return Digest(secret)
		}())
		if e != nil {
			return "", "", e
		}
		if tag.RowsAffected() != 1 {
			return "", "", fault.Conflict
		}
	}
	return v.ID, secret, nil
}
func (s *Service) OAuthClientAuthentication(ctx context.Context, id, secret string) (OAuthClient, error) {
	if id == NativeClientUUID || len(secret) != 64 {
		return OAuthClient{}, fault.Proof
	}
	var v OAuthClient
	e := s.db.Row(ctx, `SELECT `+oauthClientFields+` FROM oauth_clients WHERE id=$1 AND secret_hash=$2 AND enabled`, id, Digest(secret)).Scan(&v.ID, &v.Name, &v.RedirectURIs, &v.Scopes, &v.AllowedUsers, &v.AllowedDepartments, &v.AllowAllUsers, &v.ClientCredentials, &v.Enabled, &v.ClaimMapping, &v.Version)
	if e != nil {
		return OAuthClient{}, fault.Proof
	}
	return v, nil
}
func (s *Service) OAuthUserAllowed(ctx context.Context, c OAuthClient, user string) (bool, error) {
	var ok bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM users u WHERE id=$1 AND status='enabled' AND deleted_at IS NULL AND ($2 OR id=ANY($3::uuid[]) OR EXISTS(SELECT 1 FROM user_departments d WHERE d.user_id=u.id AND d.department_id=ANY($4::uuid[]))))`, user, c.AllowAllUsers, c.AllowedUsers, c.AllowedDepartments).Scan(&ok)
	return ok, e
}
