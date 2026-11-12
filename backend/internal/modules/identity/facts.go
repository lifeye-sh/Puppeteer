package identity

import (
	"context"
	"next-terminal/backend/internal/platform/postgres"
)

type SubjectFacts struct {
	User          User
	DepartmentIDs []string
	Enabled       bool
}

func LoadSubject(ctx context.Context, db *postgres.DB, id string) (v SubjectFacts, e error) {
	e = db.Row(ctx, "SELECT id::text,username,nickname,type,status='enabled' AND deleted_at IS NULL FROM users WHERE id=$1", id).Scan(&v.User.ID, &v.User.Username, &v.User.Nickname, &v.User.Type, &v.Enabled)
	if e != nil {
		return
	}
	v.DepartmentIDs, e = (&Service{db: db}).DepartmentIDs(ctx, id)
	return
}
func (s *Service) Subject(ctx context.Context, id string) (SubjectFacts, error) {
	return LoadSubject(ctx, s.db, id)
}
