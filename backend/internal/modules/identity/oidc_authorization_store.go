package identity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"time"
)

func (s *Service) OIDCClientSecret(ctx context.Context, id string, vaultKeys ...[]byte) (string, error) {
	var managed bool
	var ref, kind string
	if e := s.db.Row(ctx, `SELECT settings_managed,coalesce(configuration_secret_id::text,''),kind FROM oidc_identity_providers WHERE id=$1`, id).Scan(&managed, &ref, &kind); e != nil {
		return "", e
	}
	if managed {
		if ref == "" || len(vaultKeys) != 1 {
			return "", fault.Invalid
		}
		key := "oidc-client-secret"
		if kind == "wecom" {
			key = "wechat-work-secret"
		}
		var sealed []byte
		if e := s.db.Row(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1 AND key=$2`, ref, key).Scan(&sealed); e != nil {
			return "", e
		}
		plain, e := secretbox.Open(vaultKeys[0], sealed, "settings:"+key+":"+ref)
		defer clear(plain)
		return string(plain), e
	}

	var sealed []byte
	if err := s.db.Row(ctx, `SELECT secret_sealed FROM oidc_identity_providers WHERE id=$1`, id).Scan(&sealed); err != nil {
		return "", err
	}
	plain, err := secretbox.Open(s.key, sealed, "oidc-provider:"+id)
	defer clear(plain)
	return string(plain), err
}

// SaveOIDCAuthorization runs in the initiating user's audited security transaction.
func (s *Service) SaveOIDCAuthorization(ctx context.Context, user, binding, purpose string, p OIDCProvider, c OIDCAuthorization) error {
	if user == "" || binding == "" || (purpose != "test" && purpose != "bind") || len(c.State) != 64 || (p.Kind != "wecom" && (len(c.Nonce) != 64 || len(c.Verifier) < 43 || len(c.Verifier) > 128)) || !c.ExpiresAt.After(time.Now()) || c.ExpiresAt.After(time.Now().Add(5*time.Minute+time.Second)) {
		return fault.Invalid
	}
	if purpose == "bind" {
		if err := s.db.Row(ctx, `SELECT version FROM users WHERE id=$1`, user).Scan(&c.UserVersion); err != nil {
			return err
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	defer clear(data)
	sealed, err := secretbox.Seal(s.key, data, "oidc-authorization:"+Digest(c.State))
	if err != nil {
		return err
	}
	// Bound both persistent storage and concurrent outstanding challenges per user.
	if _, err = s.db.Exec(ctx, `DELETE FROM oidc_authorizations WHERE expires_at<=now() OR (user_id=$1 AND binding_hash=$2)`, user, Digest(binding)); err != nil {
		return err
	}
	var count int
	if err = s.db.Row(ctx, `SELECT count(*) FROM oidc_authorizations WHERE user_id=$1`, user).Scan(&count); err != nil {
		return err
	}
	if count >= 10 {
		return fault.Conflict
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO oidc_authorizations(state_hash,binding_hash,user_id,provider_id,provider_version,purpose,challenge_sealed,auth_epoch,expires_at) SELECT $1,$2,$3,$4,$5,$6,$7,i.auth_epoch,$8 FROM instance_state i,oidc_identity_providers p WHERE i.singleton=1 AND p.id=$4 AND p.version=$5`, Digest(c.State), Digest(binding), user, p.ID, p.Version, purpose, sealed, c.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// Delete outside the verification transaction: failure cannot restore a challenge.
func (s *Service) ConsumeOIDCAuthorization(ctx context.Context, state, binding, user, purpose string) (OIDCProvider, OIDCAuthorization, error) {
	var p OIDCProvider
	var c OIDCAuthorization
	if len(state) != 64 || binding == "" || user == "" {
		return p, c, fault.Proof
	}
	var sealed []byte
	err := s.db.Row(ctx, `DELETE FROM oidc_authorizations c USING instance_state i,oidc_identity_providers p WHERE c.state_hash=$1 AND c.binding_hash=$2 AND c.user_id=$3 AND c.purpose=$4 AND c.expires_at>now() AND c.auth_epoch=i.auth_epoch AND p.id=c.provider_id AND p.version=c.provider_version RETURNING p.id,p.version,c.challenge_sealed`, Digest(state), Digest(binding), user, purpose).Scan(&p.ID, &p.Version, &sealed)
	if err != nil {
		return p, c, fault.Proof
	}
	data, err := secretbox.Open(s.key, sealed, "oidc-authorization:"+Digest(state))
	if err != nil {
		return p, c, fault.Proof
	}
	defer clear(data)
	if json.Unmarshal(data, &c) != nil || c.State != state {
		return p, OIDCAuthorization{}, fault.Proof
	}
	return p, c, nil
}
