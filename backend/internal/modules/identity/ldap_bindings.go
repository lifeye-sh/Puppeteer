package identity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

func (s *Service) LDAPIdentities(ctx context.Context, user string) ([]ExternalIdentity, error) {
	rows, e := s.db.Rows(ctx, `SELECT e.id,e.provider_id,p.name,p.url,e.subject,e.created_at FROM ldap_identities e JOIN ldap_identity_providers p ON p.id=e.provider_id WHERE e.user_id=$1 ORDER BY e.created_at`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ExternalIdentity{}
	for rows.Next() {
		var v ExternalIdentity
		if e = rows.Scan(&v.ID, &v.ProviderID, &v.Name, &v.Issuer, &v.Subject, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) BindLDAPIdentity(ctx context.Context, user string, p LDAPProvider, subject string, version int64) error {
	var current int64
	if e := s.db.Row(ctx, `SELECT version FROM users WHERE id=$1`, user).Scan(&current); e != nil {
		return e
	}
	if current != version || subject == "" {
		return fault.Proof
	}
	var id string
	if e := s.db.Row(ctx, `SELECT id FROM ldap_identity_providers WHERE id=$1 AND version=$2 AND binding_enabled FOR SHARE`, p.ID, p.Version).Scan(&id); e != nil {
		return fault.Proof
	}
	tag, e := s.db.Exec(ctx, `INSERT INTO ldap_identities(user_id,provider_id,subject) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, user, p.ID, subject)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return s.invalidateOIDCBindings(ctx, user)
}
func (s *Service) UnbindLDAPIdentity(ctx context.Context, user, id string) error {
	tag, e := s.db.Exec(ctx, `DELETE FROM ldap_identities WHERE user_id=$1 AND id=$2`, user, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return s.invalidateOIDCBindings(ctx, user)
}
func (s *Service) LDAPLoginUser(ctx context.Context, p LDAPProvider, subject string, started time.Time) (User, error) {
	var u User
	if e := s.db.Row(ctx, `SELECT u.id,u.username FROM users u JOIN ldap_identities e ON e.user_id=u.id WHERE e.provider_id=$1 AND e.subject=$2 AND e.created_at<=$3`, p.ID, subject, started).Scan(&u.ID, &u.Username); e != nil {
		return u, fault.Proof
	}
	name := u.Username
	if e := s.CheckLock(ctx, name); e != nil {
		return User{}, e
	}
	if e := s.LockUser(ctx, u.ID); e != nil {
		return User{}, e
	}
	var id string
	if e := s.db.Row(ctx, `SELECT id FROM ldap_identity_providers WHERE id=$1 AND version=$2 AND enabled FOR SHARE`, p.ID, p.Version).Scan(&id); e != nil {
		return User{}, fault.Proof
	}
	e := s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,e.id FROM users u JOIN ldap_identities e ON e.user_id=u.id WHERE u.id=$1 AND u.status='enabled' AND u.deleted_at IS NULL AND u.external_login_valid_after<$4 AND e.created_at<=$4 AND e.provider_id=$2 AND e.subject=$3`, u.ID, p.ID, subject, started).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &u.LDAPIdentityID)
	if e != nil || name != u.Username {
		return User{}, fault.Proof
	}
	u.LDAPProviderVersion = p.Version
	return u, nil
}
func (s *Service) UserVersion(ctx context.Context, user string) (int64, error) {
	var v int64
	e := s.db.Row(ctx, `SELECT version FROM users WHERE id=$1`, user).Scan(&v)
	return v, e
}
