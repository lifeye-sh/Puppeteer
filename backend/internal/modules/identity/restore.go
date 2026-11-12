package identity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/system"
)

// QuarantineRestore revokes sessions and recovery challenges in an isolated restore.
func (s *Service) QuarantineRestore(ctx context.Context) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.QuarantineRestore(c) })
	}
	if _, e := s.db.Exec(ctx, `UPDATE configuration_nodes SET last_seen='epoch'`); e != nil {
		return e
	}
	if e := s.EnablePasswordLogin(ctx); e != nil {
		return e
	}
	ctx = system.WithDatabaseDMLConfigurationProof(ctx)
	ctx = system.WithCaptchaConfigurationProof(ctx)
	if _, e := system.New(s.db).SaveConfiguration(system.WithNativePasswordConfigurationProof(system.WithOAuthServerConfigurationProof(system.WithFederationConfigurationProof(system.WithPasskeyConfigurationProof(system.WithLDAPConfigurationProof(system.WithForwardConfigurationProof(system.WithRecordingConfigurationProof(ctx))))))), nil, map[string]json.RawMessage{system.LoginCaptchaKey: json.RawMessage("true"), system.DatabaseBlockDMLKey: json.RawMessage("true"), system.NativePasswordDisabledKey: json.RawMessage("true"), system.OAuthSigningKey: json.RawMessage(`""`), system.OAuthHMACKey: json.RawMessage(`""`), system.OAuthServerEnabledKey: json.RawMessage("false"), system.NativeClientEnabledKey: json.RawMessage("false"), "recording-enabled": json.RawMessage("true"), system.ForwardEnabledKey: json.RawMessage("false"), "oidc-enabled": json.RawMessage("false"), "oidc-binding-enabled": json.RawMessage("false"), "wechat-work-enabled": json.RawMessage("false"), "wechat-work-binding-enabled": json.RawMessage("false"), "passkey-enabled": json.RawMessage("false"), "ldap-enabled": json.RawMessage("false"), "ldap-binding-enabled": json.RawMessage("false")}, ""); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM native_ssh_gateway_keys`); e != nil {
		return e
	}
	for _, q := range []string{`DELETE FROM session_access_proofs`, `DELETE FROM identity_login_proofs`, `UPDATE identity_contact_settings SET recovery_enabled=false,sms_login_enabled=false,version=version+1`, `DELETE FROM contact_challenges`, `DELETE FROM ldap_sync_previews`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}

	for _, q := range []string{`UPDATE native_forward_settings SET enabled=false,version=version+1`, `UPDATE oauth_server_settings SET enabled=false,native_enabled=false,version=version+1`, `UPDATE oauth_clients SET enabled=false,version=version+1`, `UPDATE oauth_token_families SET revoked_at=now()`, `DELETE FROM oauth_codes`, `DELETE FROM oauth_pending_authorizations`, `UPDATE oauth_signing_keys SET active=false,retire_at=now()`, `UPDATE oauth_hmac_keys SET active=false`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}

	if _, e := s.db.Exec(ctx, `UPDATE ldap_identity_providers SET enabled=false,binding_enabled=false,version=version+1`); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM oidc_login_authorizations`); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM oidc_authorizations`); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `UPDATE oidc_identity_providers SET enabled=false,binding_enabled=false,version=version+1`); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `UPDATE passkey_credentials SET revoked_at=now() WHERE revoked_at IS NULL`); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM passkey_ceremonies`); e != nil {
		return e
	}
	for _, table := range []string{"personal_api_keys", "personal_ssh_keys"} {
		if _, e := s.db.Exec(ctx, `UPDATE `+table+` SET revoked_at=now() WHERE revoked_at IS NULL`); e != nil {
			return e
		}
	}
	for _, q := range []string{`DELETE FROM database_passwords`, `UPDATE auth_sessions SET revoked_at=now() WHERE revoked_at IS NULL`, `DELETE FROM auth_challenges`, `UPDATE mfa_factors SET pending_cipher=NULL,pending_expires_at=NULL`, `UPDATE mfa_recovery_codes SET used_at=now() WHERE used_at IS NULL`} {
		if _, e := s.db.Exec(ctx, q); e != nil {
			return e
		}
	}
	return nil
}
