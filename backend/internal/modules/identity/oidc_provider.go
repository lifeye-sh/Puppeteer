package identity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/federationconfig"
	"next-terminal/backend/internal/platform/secretbox"
)

type OIDCProvider = federationconfig.Provider

func ValidateOIDCProvider(v OIDCProvider) error { return federationconfig.Validate(v) }
func (s *Service) OIDCProviders(ctx context.Context) ([]OIDCProvider, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,issuer,client_id,redirect_uri,enabled,binding_enabled,(secret_sealed IS NOT NULL OR configuration_secret_id IS NOT NULL),version,kind,agent_id,authorization_url,scopes,settings_managed FROM oidc_identity_providers ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []OIDCProvider{}
	for rows.Next() {
		var v OIDCProvider
		if e = rows.Scan(&v.ID, &v.Name, &v.Issuer, &v.ClientID, &v.RedirectURI, &v.Enabled, &v.BindingEnabled, &v.HasSecret, &v.Version, &v.Kind, &v.AgentID, &v.AuthorizationURL, &v.Scopes, &v.Managed); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SaveOIDCProvider requires an audited transaction. Issuer is immutable: external
// identities are identified by exact issuer+subject, never by email address.
func (s *Service) SaveOIDCProvider(ctx context.Context, v OIDCProvider, secret string) (string, error) {
	if v.Managed {
		return "", fault.Denied
	}
	if v.ID != "" {
		var managed bool
		var scopes []string
		if e := s.db.Row(ctx, `SELECT settings_managed,scopes FROM oidc_identity_providers WHERE id=$1`, v.ID).Scan(&managed, &scopes); e != nil {
			return "", e
		}
		if managed {
			return "", fault.Denied
		}
		if v.Scopes == nil {
			v.Scopes = scopes
		}
	}
	if v.Scopes == nil {
		v.Scopes = []string{"openid"}
	}
	if v.Kind == "" {
		v.Kind = "oidc"
	}
	if e := ValidateOIDCProvider(v); e != nil {
		return "", e
	}
	if len(secret) > 8192 {
		return "", fault.Invalid
	}
	if v.ID == "" {
		if secret == "" {
			return "", fault.Invalid
		}
		if e := s.db.Row(ctx, `INSERT INTO oidc_identity_providers(name,issuer,client_id,redirect_uri,kind,agent_id,authorization_url,scopes) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, v.Name, v.Issuer, v.ClientID, v.RedirectURI, v.Kind, v.AgentID, v.AuthorizationURL, v.Scopes).Scan(&v.ID); e != nil {
			return "", e
		}
	} else {
		tag, e := s.db.Exec(ctx, `UPDATE oidc_identity_providers SET name=$2,client_id=$3,redirect_uri=$4,agent_id=$8,authorization_url=$9,scopes=$10,version=version+1 WHERE id=$1 AND NOT settings_managed AND version=$5 AND issuer=$6 AND kind=$7 AND (kind='oidc' OR client_id=$3)`, v.ID, v.Name, v.ClientID, v.RedirectURI, v.Version, v.Issuer, v.Kind, v.AgentID, v.AuthorizationURL, v.Scopes)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
	}
	if secret != "" {
		sealed, e := secretbox.Seal(s.key, []byte(secret), "oidc-provider:"+v.ID)
		if e != nil {
			return "", e
		}
		if _, e = s.db.Exec(ctx, `UPDATE oidc_identity_providers SET secret_sealed=$2 WHERE id=$1`, v.ID, sealed); e != nil {
			return "", e
		}
	}
	if _, e := s.db.Exec(ctx, `UPDATE oidc_identity_providers SET binding_enabled=$2,enabled=$3 WHERE id=$1`, v.ID, v.BindingEnabled, v.Enabled); e != nil {
		return "", e
	}
	return v.ID, nil
}
