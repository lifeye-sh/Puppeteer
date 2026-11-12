package identity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

type LoginPolicy struct {
	PasswordEnabled bool  `json:"passwordEnabled"`
	Version         int64 `json:"version"`
}

func (s *Service) LoginPolicy(ctx context.Context) (LoginPolicy, error) {
	var v LoginPolicy
	e := s.db.Row(ctx, `SELECT password_enabled,version FROM identity_login_policy WHERE singleton=1`).Scan(&v.PasswordEnabled, &v.Version)
	return v, e
}
func (s *Service) SaveLoginPolicy(ctx context.Context, v LoginPolicy, actor string) error {
	if _, e := s.db.Exec(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`); e != nil {
		return e
	}
	var version int64
	if e := s.db.Row(ctx, `SELECT version FROM identity_login_policy WHERE singleton=1 FOR UPDATE`).Scan(&version); e != nil {
		return e
	}
	if version != v.Version {
		return fault.Conflict
	}
	raw, _ := json.Marshal(!v.PasswordEnabled)
	_, e := system.New(s.db).SaveConfiguration(system.WithLoginPolicyProof(ctx), nil, map[string]json.RawMessage{"disable-password-login": raw}, actor)
	return e
}

func (s *Service) PasswordLoginEnabled(ctx context.Context) (bool, error) {
	v, e := system.New(s.db).SecurityConfiguration(ctx)
	if e != nil {
		return false, e
	}
	return !v.Values.Bool("disable-password-login"), nil
}
func (s *Service) RecordLoginProof(ctx context.Context, u *User, epoch int64) error {
	if u.Phase != "authenticated" {
		return nil
	}
	kind, id, version, config := "", "", int64(0), int64(0)
	switch {
	case u.ExternalIdentityID != "":
		kind, id, version = "oidc", u.ExternalIdentityID, u.OIDCProviderVersion
	case u.LDAPIdentityID != "":
		kind, id, version = "ldap", u.LDAPIdentityID, u.LDAPProviderVersion
	case u.ContactID != "":
		kind, id, version, config = "sms", u.ContactID, u.ContactVersion, u.ContactSettingsVersion
	case u.PasskeyID != "":
		kind, id = "passkey", u.PasskeyID
	}
	if id == "" {
		return nil
	}
	_, e := s.db.Exec(ctx, `INSERT INTO identity_login_proofs(user_id,kind,credential_id,source_version,config_version,auth_epoch,channel_version) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id,kind,credential_id) DO UPDATE SET source_version=EXCLUDED.source_version,config_version=EXCLUDED.config_version,auth_epoch=EXCLUDED.auth_epoch,channel_version=EXCLUDED.channel_version,verified_at=now()`, u.ID, kind, id, version, config, epoch, u.ContactChannelVersion)
	return e
}
func (s *Service) AdminLoginAlternatives(ctx context.Context, user string, recent bool, rp string) (int, error) {
	var count int
	e := s.db.Row(ctx, `SELECT count(DISTINCT u.id) FROM identity_login_proofs v JOIN users u ON u.id=v.user_id CROSS JOIN instance_state i WHERE u.type='admin' AND u.status='enabled' AND u.deleted_at IS NULL AND ($1='' OR u.id::text=$1) AND (NOT $2 OR v.verified_at>now()-interval '15 minutes') AND v.auth_epoch=i.auth_epoch AND v.verified_at>=u.external_login_valid_after AND (
(v.kind='oidc' AND EXISTS(SELECT 1 FROM external_identities e JOIN oidc_identity_providers p ON p.id=e.provider_id WHERE e.id=v.credential_id AND e.user_id=u.id AND p.enabled AND p.version=v.source_version)) OR
(v.kind='ldap' AND EXISTS(SELECT 1 FROM ldap_identities e JOIN ldap_identity_providers p ON p.id=e.provider_id WHERE e.id=v.credential_id AND e.user_id=u.id AND p.enabled AND p.version=v.source_version)) OR
(v.kind='sms' AND EXISTS(SELECT 1 FROM verified_contacts c CROSS JOIN identity_contact_settings s WHERE c.id=v.credential_id AND c.user_id=u.id AND c.kind='phone' AND c.version=v.source_version AND s.sms_login_enabled AND s.version=v.config_version AND EXISTS(SELECT 1 FROM notification_channels n WHERE n.id=s.sms_channel AND n.enabled AND n.version=v.channel_version))) OR
(v.kind='passkey' AND $3<>'' AND EXISTS(SELECT 1 FROM passkey_credentials p WHERE p.id=v.credential_id AND p.user_id=u.id AND p.revoked_at IS NULL AND p.rp_id=$3)))`, user, recent, rp).Scan(&count)
	return count, e
}

// Serialize the final non-locking invariant check, after changes but before commit.
// Under READ COMMITTED the next mutator observes the previous commit; two parallel
// administrators cannot each remove the other's final alternate credential.
func (s *Service) AssertAdminLoginPath(ctx context.Context, rp string) error {
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723048)`); e != nil {
		return e
	}
	policy, e := s.LoginPolicy(ctx)
	if e != nil || policy.PasswordEnabled {
		return e
	}
	count, e := s.AdminLoginAlternatives(ctx, "", false, rp)
	if e != nil {
		return e
	}
	if count == 0 {
		return fault.Conflict
	}
	return nil
}
func (s *Service) EnablePasswordLogin(ctx context.Context) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.EnablePasswordLogin(c) })
	}
	_, e := system.New(s.db).SaveConfiguration(system.WithLoginPolicyProof(ctx), nil, map[string]json.RawMessage{"disable-password-login": json.RawMessage("false")}, "")
	if e != nil {
		return e
	}
	// Explicit local break-glass and isolated recovery must remain usable even
	// when consumers cannot ACK. No other security restriction is relaxed.
	_, e = s.db.Exec(ctx, `UPDATE configuration_security SET enforced=jsonb_set(enforced,'{disable-password-login}','false') WHERE singleton=1`)
	return e
}
func (s *Service) PasskeyRecordID(ctx context.Context, user, rp string, raw []byte) (string, error) {
	var id string
	e := s.db.Row(ctx, `SELECT id FROM passkey_credentials WHERE user_id=$1 AND rp_id=$2 AND credential_id=$3 AND revoked_at IS NULL`, user, rp, raw).Scan(&id)
	return id, e
}
