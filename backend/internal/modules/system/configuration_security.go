package system

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"reflect"
)

// These values are read from the database at every authorization/password check,
// never from a process cache. Tightening is immediate. During a rollout, the
// intersection retains every restriction until the required live consumers ACK.
var securityKeys = []string{LoginCaptchaKey, DatabaseBlockDMLKey, NativePasswordDisabledKey, "access-require-mfa", "access-mfa-expires-at", OAuthServerEnabledKey, NativeClientEnabledKey, "recording-enabled", ForwardEnabledKey, ForwardRulesKey, "oidc-enabled", "oidc-binding-enabled", "wechat-work-enabled", "wechat-work-binding-enabled", "passkey-enabled", "access-max-idle-second", "ldap-enabled", "ldap-binding-enabled", "disable-password-login", "ip-extractor", "ip-trust-list", "login-lock-enabled", "login-lock-failed-duration", "login-lock-failed-times", "login-lock-account-duration", "login-lock-ip-duration", "login-force-totp-enabled", "password-expiration-period", "password-strength-type", "password-strength-policy"}

func securityValues(v ConfigurationValues) ConfigurationValues {
	out := ConfigurationValues{}
	for _, k := range securityKeys {
		out[k] = v[k]
	}
	return out
}

func effectivePasswordPolicy(v ConfigurationValues) (PasswordPolicy, error) {
	if v.String("password-strength-type") == "recommend" {
		return PasswordPolicy{MinLength: 12, MinCharacterType: 1, MustNotContainUsername: true, MustNotWeak: true}, nil
	}
	return ParsePasswordPolicy(v.String("password-strength-policy"))
}

func restrictiveSecurity(old, next ConfigurationValues) (ConfigurationValues, error) {
	out := securityValues(next)
	out["access-require-mfa"] = old.Bool("access-require-mfa") || next.Bool("access-require-mfa")
	out["access-mfa-expires-at"] = min(old.Int("access-mfa-expires-at"), next.Int("access-mfa-expires-at"))
	out["recording-enabled"] = old.Bool("recording-enabled") || next.Bool("recording-enabled")
	out[ForwardEnabledKey] = old.Bool(ForwardEnabledKey) && next.Bool(ForwardEnabledKey) && old.String(ForwardRulesKey) == next.String(ForwardRulesKey)
	idle := next.Int("access-max-idle-second")
	if previous := old.Int("access-max-idle-second"); previous > 0 && (idle < 0 || previous < idle) {
		idle = previous
	}
	out["access-max-idle-second"] = idle
	for _, k := range []string{OAuthServerEnabledKey, NativeClientEnabledKey, "oidc-enabled", "oidc-binding-enabled", "wechat-work-enabled", "wechat-work-binding-enabled", "passkey-enabled", "ldap-enabled", "ldap-binding-enabled"} {
		out[k] = old.Bool(k) && next.Bool(k)
	}
	if e := restrictiveClientIP(old, next, out); e != nil {
		return nil, e
	}
	for _, k := range []string{LoginCaptchaKey, DatabaseBlockDMLKey, NativePasswordDisabledKey, "disable-password-login", "login-lock-enabled", "login-force-totp-enabled"} {
		out[k] = old.Bool(k) || next.Bool(k)
	}
	for _, k := range []string{"login-lock-failed-duration", "login-lock-account-duration", "login-lock-ip-duration"} {
		out[k] = max(old.Int(k), next.Int(k))
	}
	out["login-lock-failed-times"] = min(old.Int("login-lock-failed-times"), next.Int("login-lock-failed-times"))
	expiry := next.Int("password-expiration-period")
	if previous := old.Int("password-expiration-period"); previous > 0 && (expiry < 0 || previous < expiry) {
		expiry = previous
	}
	out["password-expiration-period"] = expiry
	a, e := effectivePasswordPolicy(old)
	if e != nil {
		return nil, e
	}
	b, e := effectivePasswordPolicy(next)
	if e != nil {
		return nil, e
	}
	merged := PasswordPolicy{MinLength: max(a.MinLength, b.MinLength), MinCharacterType: max(a.MinCharacterType, b.MinCharacterType), MustNotContainUsername: a.MustNotContainUsername || b.MustNotContainUsername, MustNotWeak: a.MustNotWeak || b.MustNotWeak, MustNotBePalindrome: a.MustNotBePalindrome || b.MustNotBePalindrome}
	if merged != b {
		raw, e := json.Marshal(merged)
		if e != nil {
			return nil, e
		}
		out["password-strength-type"] = "customize"
		out["password-strength-policy"] = string(raw)
	}
	return out, nil
}

func (s *Service) publishSecurityConfiguration(ctx context.Context, id int64, next ConfigurationValues) error {
	var raw []byte
	if e := s.db.Row(ctx, `SELECT enforced FROM configuration_security WHERE singleton=1 FOR UPDATE`).Scan(&raw); e != nil {
		return e
	}
	var old ConfigurationValues
	if e := json.Unmarshal(raw, &old); e != nil {
		return e
	}
	enforced, e := restrictiveSecurity(old, next)
	if e != nil {
		return e
	}
	pending := !reflect.DeepEqual(enforced, securityValues(next))
	// No process has registered in a new/offline deployment: all later consumers
	// must load the committed snapshot before admitting work.
	var nodes []string
	if e = s.db.Row(ctx, `SELECT coalesce(array_agg(id ORDER BY id),'{}'::text[]) FROM configuration_nodes WHERE last_seen>now()-interval '30 seconds'`).Scan(&nodes); e != nil {
		return e
	}
	if !pending || len(nodes) == 0 {
		enforced = securityValues(next)
		pending = false
		nodes = []string{}
	}
	raw, e = json.Marshal(enforced)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE configuration_security SET revision=$1,enforced=$2,required_nodes=$3,pending=$4 WHERE singleton=1`, id, raw, nodes, pending)
	if e == nil {
		e = s.enforceLDAPConfiguration(ctx)
	}
	if e == nil {
		e = s.enforceFederationConfiguration(ctx)
	}
	if e == nil {
		e = s.enforceForwardConfiguration(ctx)
	}
	if e == nil {
		e = s.enforceOAuthServerConfiguration(ctx)
	}
	return e
}

// Called after the ACK write in the same transaction. A stopped/partitioned
// process loses membership after its 30-second heartbeat lease; it cannot read
// old cached policy when reconnecting, because all security decisions read here.
func (s *Service) activateSecurityConfiguration(ctx context.Context, v Revision) error {
	var pending bool
	if e := s.db.Row(ctx, `SELECT pending FROM configuration_security WHERE singleton=1 AND revision=$1 FOR UPDATE`, v.ID).Scan(&pending); e != nil {
		return e
	}
	if !pending {
		return nil
	}
	var blocked bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM configuration_security s CROSS JOIN unnest(s.required_nodes) required(id) JOIN configuration_nodes n ON n.id=required.id WHERE s.singleton=1 AND n.last_seen>now()-interval '30 seconds' AND NOT EXISTS(SELECT 1 FROM configuration_acks a WHERE a.revision=s.revision AND a.node_id=n.id AND a.state='effective'))`).Scan(&blocked); e != nil {
		return e
	}
	if blocked {
		return nil
	}
	raw, e := json.Marshal(securityValues(v.Values))
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE configuration_security SET enforced=$1,pending=false,required_nodes='{}' WHERE singleton=1 AND revision=$2`, raw, v.ID)
	if e == nil {
		e = s.enforceLDAPConfiguration(ctx)
	}
	if e == nil {
		e = s.enforceFederationConfiguration(ctx)
	}
	if e == nil {
		e = s.enforceForwardConfiguration(ctx)
	}
	if e == nil {
		e = s.enforceOAuthServerConfiguration(ctx)
	}
	return e
}

// One statement captures head and enforcement together, including during a save.
func (s *Service) SecurityConfiguration(ctx context.Context) (v Revision, e error) {
	var raw, enforced []byte
	e = s.db.Row(ctx, `SELECT r.id,r.snapshot,s.enforced,r.changed_keys,coalesce(u.username,''),r.legacy_write,r.created_at FROM configuration_head h JOIN configuration_revisions r ON r.id=h.revision JOIN configuration_security s ON s.singleton=1 AND s.revision=r.id LEFT JOIN users u ON u.id=r.actor_id`).Scan(&v.ID, &raw, &enforced, &v.ChangedKeys, &v.Actor, &v.Legacy, &v.CreatedAt)
	if e != nil {
		return
	}
	if e = json.Unmarshal(raw, &v.Values); e != nil {
		return
	}
	// An enforcement overlay must not conceal a missing or malformed desired
	// property. Corrupt configuration always denies authorization.
	if e = ValidateConfiguration(v.Values); e != nil {
		return
	}
	var floor ConfigurationValues
	if e = json.Unmarshal(enforced, &floor); e != nil {
		return
	}
	if len(floor) != len(securityKeys) {
		e = fault.Invalid
		return
	}
	for _, key := range securityKeys {
		value, ok := floor[key]
		if !ok {
			e = fault.Invalid
			return
		}
		v.Values[key] = value
	}
	e = ValidateConfiguration(v.Values)
	return
}
