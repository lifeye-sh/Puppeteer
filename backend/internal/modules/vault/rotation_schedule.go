package vault

import "context"

// AttachScheduledRotation uses the scheduler job as the rotation identity, so
// cancellation, leases and trigger deduplication are shared, not duplicated.
func (s *Service) AttachScheduledRotation(ctx context.Context, id, actor, ip string) error {
	_, e := s.db.Exec(ctx, `INSERT INTO rotation_runs(id,actor_id,request_key,source_ip) VALUES($1,$2,$1,$3)`, id, actor, ip)
	return e
}
