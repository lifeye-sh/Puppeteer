package identity

import (
	"context"
	"next-terminal/backend/internal/modules/system"
)

func (u User) UsesLocalPassword() bool {
	return u.ExternalIdentityID == "" && u.LDAPIdentityID == "" && u.ContactID == "" && u.PasskeyID == ""
}

func (s *Service) PasswordExpired(ctx context.Context, id string) (expired bool, err error) {
	configuration, err := system.New(s.db).SecurityConfiguration(ctx)
	if err != nil {
		return false, err
	}
	err = s.db.Row(ctx, `SELECT password_hash LIKE '$argon2id$%' AND $2::integer>0 AND password_changed_at+$2::integer*interval '1 day'<=now() FROM users WHERE id=$1`, id, configuration.Values.Int("password-expiration-period")).Scan(&expired)
	return
}

func (s *Service) ChangePassword(ctx context.Context, id, hash string) error {
	if _, err := s.db.Exec(ctx, `UPDATE users SET password_hash=$2,version=version+1 WHERE id=$1`, id, hash); err != nil {
		return err
	}
	if err := s.RevokeDatabasePassword(ctx, id); err != nil {
		return err
	}
	return s.RevokeAll(ctx, id)
}
