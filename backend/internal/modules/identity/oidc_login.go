package identity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"time"
)

type OIDCLoginChallenge struct {
	ProviderID      string
	ProviderVersion int64
	AuthEpoch       int64
	CreatedAt       time.Time
	Authorization   OIDCAuthorization
}

// This short transaction serializes the outstanding global limit, with no network IO.
func (s *Service) SaveOIDCLogin(ctx context.Context, binding string, p OIDCProvider, c OIDCAuthorization) error {
	if len(binding) != 64 || len(c.State) != 64 || !c.ExpiresAt.After(time.Now()) || c.ExpiresAt.After(time.Now().Add(5*time.Minute+time.Second)) {
		return fault.Invalid
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	defer clear(data)
	sealed, err := secretbox.Seal(s.key, data, "oidc-login:"+Digest(c.State))
	if err != nil {
		return err
	}
	return s.db.Within(ctx, func(ctx context.Context) error {
		if _, err := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723041)`); err != nil {
			return err
		}
		if _, err := s.db.Exec(ctx, `DELETE FROM oidc_login_authorizations WHERE expires_at<=now() OR binding_hash=$1`, Digest(binding)); err != nil {
			return err
		}
		var count int
		if err := s.db.Row(ctx, `SELECT count(*) FROM oidc_login_authorizations`).Scan(&count); err != nil {
			return err
		}
		if count >= 1000 {
			return fault.Conflict
		}
		tag, err := s.db.Exec(ctx, `INSERT INTO oidc_login_authorizations(state_hash,binding_hash,provider_id,provider_version,challenge_sealed,auth_epoch,expires_at) SELECT $1,$2,p.id,p.version,$5,i.auth_epoch,$6 FROM oidc_identity_providers p,instance_state i WHERE p.id=$3 AND p.version=$4 AND p.enabled AND i.singleton=1`, Digest(c.State), Digest(binding), p.ID, p.Version, sealed, c.ExpiresAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fault.Proof
		}
		return nil
	})
}

func (s *Service) HasOIDCLogin(ctx context.Context, state string) (bool, error) {
	if len(state) != 64 {
		return false, nil
	}
	var exists bool
	err := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM oidc_login_authorizations WHERE state_hash=$1)`, Digest(state)).Scan(&exists)
	return exists, err
}

// Must execute outside the session-issuing transaction; bad responses cannot replay.
func (s *Service) ConsumeOIDCLogin(ctx context.Context, state, binding string) (OIDCLoginChallenge, error) {
	var c OIDCLoginChallenge
	var sealed []byte
	if len(state) != 64 || len(binding) != 64 {
		return c, fault.Proof
	}
	err := s.db.Row(ctx, `DELETE FROM oidc_login_authorizations c USING instance_state i WHERE c.state_hash=$1 AND c.binding_hash=$2 AND c.expires_at>now() AND c.auth_epoch=i.auth_epoch RETURNING c.provider_id,c.provider_version,c.challenge_sealed,c.auth_epoch,c.created_at`, Digest(state), Digest(binding)).Scan(&c.ProviderID, &c.ProviderVersion, &sealed, &c.AuthEpoch, &c.CreatedAt)
	if err != nil {
		return c, fault.Proof
	}
	data, err := secretbox.Open(s.key, sealed, "oidc-login:"+Digest(state))
	if err != nil {
		return c, fault.Proof
	}
	defer clear(data)
	if json.Unmarshal(data, &c.Authorization) != nil || c.Authorization.State != state {
		return c, fault.Proof
	}
	return c, nil
}

// Resolve only pre-existing bindings, under the same user lock as unbind/revoke.
func (s *Service) OIDCLoginUser(ctx context.Context, c OIDCLoginChallenge, subject OIDCSubject) (User, error) {
	var u User
	if err := s.db.Row(ctx, `SELECT e.user_id,u.username FROM external_identities e JOIN users u ON u.id=e.user_id WHERE e.provider_id=$1 AND e.issuer=$2 AND e.subject=$3 AND e.created_at<=$4`, c.ProviderID, subject.Issuer, subject.Subject, c.CreatedAt).Scan(&u.ID, &u.Username); err != nil {
		return u, fault.Proof
	}
	// Match password login's lock order: login-name advisory lock, then user row.
	initialName := u.Username
	if err := s.CheckLock(ctx, initialName); err != nil {
		return User{}, err
	}
	if err := s.LockUser(ctx, u.ID); err != nil {
		return User{}, fault.Proof
	}
	// Lock provider after user, in the same order as administrative edits.
	var id string
	if err := s.db.Row(ctx, `SELECT id FROM oidc_identity_providers WHERE id=$1 AND issuer=$2 AND enabled AND version=$3 FOR SHARE`, c.ProviderID, subject.Issuer, c.ProviderVersion).Scan(&id); err != nil {
		return User{}, fault.Proof
	}
	err := s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,e.id FROM users u JOIN external_identities e ON e.user_id=u.id WHERE u.id=$1 AND u.status='enabled' AND u.deleted_at IS NULL AND u.external_login_valid_after<$5 AND e.created_at<=$5 AND e.provider_id=$2 AND e.issuer=$3 AND e.subject=$4`, u.ID, c.ProviderID, subject.Issuer, subject.Subject, c.CreatedAt).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &u.ExternalIdentityID)
	if err != nil {
		return User{}, fault.Proof
	}
	if u.Username != initialName {
		return User{}, fault.Proof
	}
	u.OIDCProviderVersion = c.ProviderVersion
	return u, nil
}
