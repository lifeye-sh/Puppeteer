package system

import (
	"context"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

const OAuthServerEnabledKey = "oidc-server-enabled"
const OAuthServerIssuerKey = "oidc-server-issuer"
const NativeClientEnabledKey = "native-client-enabled"
const OAuthSigningKey = "oidc-server-signing-key"
const OAuthHMACKey = "oidc-server-hmac-secret"

type oauthServerProofKey struct{}

func WithOAuthServerConfigurationProof(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauthServerProofKey{}, true)
}
func hasOAuthServerConfigurationProof(ctx context.Context) bool {
	v, _ := ctx.Value(oauthServerProofKey{}).(bool)
	return v
}
func IsOAuthServerConfigurationKey(key string) bool {
	return IsOAuthServerEntryKey(key) || key == OAuthSigningKey || key == OAuthHMACKey
}
func validateOAuthServerConfiguration(v ConfigurationValues) error {
	issuer := v.String(OAuthServerIssuerKey)
	if issuer == "" && !v.Bool(OAuthServerEnabledKey) && !v.Bool(NativeClientEnabledKey) {
		return nil
	}
	u, e := url.Parse(issuer)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(issuer) > 512 || strings.ContainsAny(issuer, "\r\n\\#?") {
		return fault.Invalid
	}
	return nil
}
func (s *Service) applyOAuthServerConfiguration(ctx context.Context, v ConfigurationValues) error {
	if v.Bool(OAuthServerEnabledKey) || v.Bool(NativeClientEnabledKey) {
		var ready bool
		if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM oauth_signing_keys WHERE active)`).Scan(&ready); e != nil {
			return e
		}
		if !ready {
			return fault.Invalid
		}
	}
	// A published issuer is an identity boundary and cannot be silently replaced.
	// Version also advances for desired-only changes while ACKs are pending, so
	// legacy optimistic concurrency cannot overwrite a newer requested state.
	tag, e := s.db.Exec(ctx, `UPDATE oauth_server_settings SET issuer=$1,version=version+1 WHERE singleton=1 AND (issuer='' OR issuer=$1)`, v.String(OAuthServerIssuerKey))
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}
func (s *Service) enforceOAuthServerConfiguration(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE oauth_server_settings o SET enabled=(s.enforced->>'oidc-server-enabled')::boolean,native_enabled=(s.enforced->>'native-client-enabled')::boolean,version=o.version+1 FROM configuration_security s WHERE s.singleton=1 AND o.singleton=1 AND (o.enabled IS DISTINCT FROM (s.enforced->>'oidc-server-enabled')::boolean OR o.native_enabled IS DISTINCT FROM (s.enforced->>'native-client-enabled')::boolean)`)
	if e != nil {
		return e
	}
	// The fixed native client is still gated by native_enabled on every grant use.
	_, e = s.db.Exec(ctx, `UPDATE oauth_clients SET enabled=true,version=version+CASE WHEN enabled THEN 0 ELSE 1 END WHERE id='00000000-0000-4000-8000-000000000014' AND (SELECT native_enabled FROM oauth_server_settings WHERE singleton=1)`)
	return e
}

func IsOAuthServerEntryKey(key string) bool {
	return key == OAuthServerEnabledKey || key == OAuthServerIssuerKey || key == NativeClientEnabledKey
}

type oauthKeyRotationKey struct{}

func WithOAuthKeyRotation(ctx context.Context, keys []string) context.Context {
	return context.WithValue(ctx, oauthKeyRotationKey{}, append([]string(nil), keys...))
}
func OAuthKeyRotations(ctx context.Context) []string {
	keys, _ := ctx.Value(oauthKeyRotationKey{}).([]string)
	return append([]string(nil), keys...)
}
