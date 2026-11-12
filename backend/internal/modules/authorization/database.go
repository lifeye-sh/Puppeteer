package authorization

import "context"

// SSH channel and command policies do not describe SQL access. A grant carrying
// one is not silently treated as an unrestricted database grant.
func (s *Service) DatabaseAccess(ctx context.Context, user, asset string, departments []string) (bool, error) {
	if departments == nil {
		departments = []string{}
	}
	var allowed bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM grants WHERE resource_id=$1 AND revoked_at IS NULL AND valid_from<=now() AND (expires_at IS NULL OR expires_at>now()) AND strategy_id IS NULL AND command_filter_id IS NULL AND (user_id=$2 OR department_id=ANY($3::uuid[])))`, asset, user, departments).Scan(&allowed)
	return allowed, e
}
