package identity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type ExternalIdentity struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"providerId"`
	Name       string    `json:"name"`
	Issuer     string    `json:"issuer"`
	Subject    string    `json:"subject"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s *Service) ExternalIdentities(ctx context.Context, user string) ([]ExternalIdentity, error) {
	rows, err := s.db.Rows(ctx, `SELECT e.id,e.provider_id,p.name,e.issuer,e.subject,e.created_at FROM external_identities e JOIN oidc_identity_providers p ON p.id=e.provider_id WHERE e.user_id=$1 ORDER BY e.created_at`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ExternalIdentity{}
	for rows.Next() {
		var v ExternalIdentity
		if err = rows.Scan(&v.ID, &v.ProviderID, &v.Name, &v.Issuer, &v.Subject, &v.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Service) OIDCAuthorizationPurpose(ctx context.Context, state, binding, user string) (string, error) {
	var purpose string
	if len(state) != 64 || binding == "" {
		return "", fault.Proof
	}
	err := s.db.Row(ctx, `SELECT purpose FROM oidc_authorizations WHERE state_hash=$1 AND binding_hash=$2 AND user_id=$3 AND expires_at>now()`, Digest(state), Digest(binding), user).Scan(&purpose)
	if err != nil {
		return "", fault.Proof
	}
	return purpose, nil
}

// BindExternalIdentity must run under the user's lock and audited transaction.
// Only the verified issuer+subject is accepted; email and display name never link accounts.
func (s *Service) BindExternalIdentity(ctx context.Context, user string, p OIDCProvider, subject OIDCSubject, userVersion int64) error {
	if subject.Issuer != p.Issuer || subject.Subject == "" || len(subject.Subject) > 255 {
		return fault.Proof
	}
	var locked string
	if err := s.db.Row(ctx, `SELECT id FROM oidc_identity_providers WHERE id=$1 AND version=$2 AND binding_enabled AND issuer=$3 FOR SHARE`, p.ID, p.Version, subject.Issuer).Scan(&locked); err != nil {
		return fault.Proof
	}
	var version int64
	if err := s.db.Row(ctx, `SELECT version FROM users WHERE id=$1`, user).Scan(&version); err != nil {
		return err
	}
	if version != userVersion {
		return fault.Proof
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO external_identities(user_id,provider_id,issuer,subject) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, user, p.ID, subject.Issuer, subject.Subject)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return s.invalidateOIDCBindings(ctx, user)
}

func (s *Service) UnbindExternalIdentity(ctx context.Context, user, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM external_identities WHERE user_id=$1 AND id=$2`, user, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return s.invalidateOIDCBindings(ctx, user)
}

func (s *Service) invalidateOIDCBindings(ctx context.Context, user string) error {
	// Increment the locked user's version so an already-consumed, in-flight callback
	// cannot undo an unbind. Pending binding attempts are also removed immediately.
	if _, err := s.db.Exec(ctx, `UPDATE users SET version=version+1 WHERE id=$1`, user); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM oidc_authorizations WHERE user_id=$1 AND purpose='bind'`, user)
	return err
}
