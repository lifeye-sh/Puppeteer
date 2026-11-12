package identity

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type DatabasePasswordStatus struct {
	SCRAMReady bool       `json:"scramReady"`
	Exists     bool       `json:"exists"`
	Active     bool       `json:"active"`
	Version    int64      `json:"version"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	UpdatedAt  *time.Time `json:"updatedAt"`
}

func (s *Service) DatabasePrincipal(ctx context.Context, name string) (u User, version int64, e error) {
	e = s.db.Row(ctx, `SELECT u.id,u.username,u.nickname,u.type,p.version FROM users u JOIN database_passwords p ON p.user_id=u.id WHERE u.username=$1 AND u.deleted_at IS NULL AND u.status='enabled' AND p.revoked_at IS NULL AND p.expires_at>now()`, name).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &version)
	return
}

func (s *Service) DatabasePasswordStatus(ctx context.Context, id string) (v DatabasePasswordStatus, e error) {
	e = s.db.Row(ctx, `SELECT true,revoked_at IS NULL AND expires_at>now(),version,expires_at,updated_at,scram_ready AND revoked_at IS NULL AND expires_at>now() FROM database_passwords WHERE user_id=$1`, id).Scan(&v.Exists, &v.Active, &v.Version, &v.ExpiresAt, &v.UpdatedAt, &v.SCRAMReady)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return
}
func (s *Service) RevokeDatabasePassword(ctx context.Context, id string) error {
	_, e := s.db.Exec(ctx, `UPDATE database_passwords SET revoked_at=now(),ciphertext=''::bytea,wrapped_key=''::bytea,scram_ready=false,version=version+1,updated_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
	return e
}
