package identity

import "context"

func (s *Service) DepartmentIDs(ctx context.Context, user string) ([]string, error) {
	rows, e := s.db.Rows(ctx, `SELECT department_id::text FROM user_departments WHERE user_id=$1`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Service) SubjectExists(ctx context.Context, user, dept string) (bool, error) {
	var v bool
	var e error
	if user != "" {
		e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)`, user).Scan(&v)
	} else {
		e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM departments WHERE id=$1)`, dept).Scan(&v)
	}
	return v, e
}
