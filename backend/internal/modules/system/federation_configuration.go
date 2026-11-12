package system

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/federationconfig"
	"strings"
)

type federationProofKey struct{}

func WithFederationConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, federationProofKey{}, true)
}
func hasFederationConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(federationProofKey{}).(bool)
	return v
}
func IsFederationConfigurationKey(k string) bool {
	return strings.HasPrefix(k, "wechat-work-") || (strings.HasPrefix(k, "oidc-") && !strings.HasPrefix(k, "oidc-server-"))
}

func federationProvider(v ConfigurationValues, kind string) (federationconfig.Provider, string) {
	if kind == "wecom" {
		return federationconfig.Provider{Kind: kind, Name: "平台默认企业微信", Issuer: v.String("wechat-work-api-url"), AuthorizationURL: v.String("wechat-work-authorization-url"), ClientID: v.String("wechat-work-corp-id"), AgentID: v.String("wechat-work-agent-id"), RedirectURI: v.String("wechat-work-redirect-uri"), Enabled: v.Bool("wechat-work-enabled"), BindingEnabled: v.Bool("wechat-work-binding-enabled")}, v.String("wechat-work-secret")
	}
	return federationconfig.Provider{Kind: kind, Name: "平台默认 OIDC", Issuer: v.String("oidc-issuer"), ClientID: v.String("oidc-client-id"), RedirectURI: v.String("oidc-redirect-uri"), Enabled: v.Bool("oidc-enabled"), BindingEnabled: v.Bool("oidc-binding-enabled"), Scopes: strings.Fields(v.String("oidc-scopes"))}, v.String("oidc-client-secret")
}

func validateFederationConfiguration(v ConfigurationValues) error {
	for _, kind := range []string{"oidc", "wecom"} {
		p, secret := federationProvider(v, kind)
		if p.ClientID == "" && p.RedirectURI == "" && p.AgentID == "" && !p.Enabled && !p.BindingEnabled && secret == "" {
			// Placeholders only validate an unconfigured provider's optional syntax.
			// They are never persisted or sent to an identity service.
			p.ClientID = "unconfigured"
			p.RedirectURI = "https://callback.invalid/"
			if kind == "oidc" && p.Issuer == "" {
				p.Issuer = "https://issuer.invalid"
			}
			if kind == "wecom" {
				p.AgentID = "1"
			}
		}
		if federationconfig.Validate(p) != nil || ((p.Enabled || p.BindingEnabled) && secret == "") {
			return fault.Invalid
		}
	}
	return nil
}

func (s *Service) applyFederationConfiguration(ctx context.Context, v ConfigurationValues, kind string) error {
	p, secret := federationProvider(v, kind)
	var exists bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM oidc_identity_providers WHERE settings_managed AND kind=$1)`, kind).Scan(&exists); e != nil {
		return e
	}
	if !exists && p.ClientID == "" {
		return nil
	}
	if !exists {
		_, e := s.db.Exec(ctx, `INSERT INTO oidc_identity_providers(name,issuer,client_id,redirect_uri,kind,agent_id,authorization_url,scopes,settings_managed,configuration_secret_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true,NULLIF($9,'')::uuid)`, p.Name, p.Issuer, p.ClientID, p.RedirectURI, kind, p.AgentID, p.AuthorizationURL, p.RequestedScopes(), secret)
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE oidc_identity_providers SET client_id=$1,redirect_uri=$2,agent_id=$3,authorization_url=$4,scopes=$5,configuration_secret_id=NULLIF($6,'')::uuid,version=version+1 WHERE settings_managed AND kind=$7 AND issuer=$8 AND (kind='oidc' OR client_id=$1)`, p.ClientID, p.RedirectURI, p.AgentID, p.AuthorizationURL, p.RequestedScopes(), secret, kind, p.Issuer)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

func (s *Service) enforceFederationConfiguration(ctx context.Context) error {
	for _, pair := range [][2]string{{"oidc", "oidc"}, {"wecom", "wechat-work"}} {
		_, e := s.db.Exec(ctx, `UPDATE oidc_identity_providers p SET version=p.version+1,enabled=(s.enforced->>$2)::boolean,binding_enabled=(s.enforced->>$3)::boolean FROM configuration_security s WHERE s.singleton=1 AND p.settings_managed AND p.kind=$1 AND (p.enabled IS DISTINCT FROM (s.enforced->>$2)::boolean OR p.binding_enabled IS DISTINCT FROM (s.enforced->>$3)::boolean)`, pair[0], pair[1]+"-enabled", pair[1]+"-binding-enabled")
		if e != nil {
			return e
		}
	}
	return nil
}
