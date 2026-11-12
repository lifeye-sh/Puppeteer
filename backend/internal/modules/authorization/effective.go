package authorization

import "context"

func (s *Service) ForcedPolicies(ctx context.Context, user string, departments, resources []string) ([]string, error) {
	if departments == nil {
		departments = []string{}
	}
	if resources == nil {
		resources = []string{}
	}
	rows, e := s.db.Rows(ctx, `SELECT coalesce(strategy_id::text,''),coalesce(command_filter_id::text,'') FROM grants WHERE revoked_at IS NULL AND valid_from<=now() AND (expires_at IS NULL OR expires_at>now()) AND (user_id=$1 OR department_id=ANY($2::uuid[])) AND resource_id=ANY($3::uuid[])`, user, departments, resources)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a, b string
		if e = rows.Scan(&a, &b); e != nil {
			return nil, e
		}
		if a != "" {
			out = append(out, a)
		}
		if b != "" {
			out = append(out, b)
		}
	}
	return out, rows.Err()
}
func (s *Service) RetireExpiredGrant(ctx context.Context, user, resource string) error {
	_, e := s.db.Exec(ctx, "UPDATE grants SET revoked_at=now(),version=version+1 WHERE user_id=$1 AND resource_id=$2 AND revoked_at IS NULL AND expires_at<=now()", user, resource)
	return e
}
