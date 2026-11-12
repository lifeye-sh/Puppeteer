package system

import "context"

func (s *Service) QuarantineRestore(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE instance_state SET auth_epoch=auth_epoch+1 WHERE singleton=1`)
	if e != nil {
		return e
	}
	// Keep unified mail settings consistent with notification quarantine. This
	// appends a recovery revision instead of rewriting the source history.
	_, e = s.db.Exec(ctx, `WITH added AS (
 INSERT INTO configuration_revisions(snapshot,changed_keys)
 SELECT r.snapshot||'{"mail-enabled":false,"ssh-server-private-key":""}'::jsonb,
 array_remove(ARRAY[CASE WHEN r.snapshot->'mail-enabled'='true'::jsonb THEN 'mail-enabled' END,CASE WHEN coalesce(r.snapshot->>'ssh-server-private-key','')<>'' THEN 'ssh-server-private-key' END],NULL)
 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id
 WHERE r.snapshot->'mail-enabled'='true'::jsonb OR coalesce(r.snapshot->>'ssh-server-private-key','')<>'' RETURNING id
 ) UPDATE configuration_head SET revision=(SELECT id FROM added) WHERE EXISTS(SELECT 1 FROM added)`)
	if e != nil {
		return e
	}
	if _, e = s.db.Exec(ctx, `UPDATE settings SET typed_value='false',revision=revision+1 WHERE key='mail-enabled' AND typed_value='true'::jsonb`); e != nil {
		return e
	}
	if _, e = s.db.Exec(ctx, `UPDATE settings SET typed_value='""',revision=revision+1 WHERE key='ssh-server-private-key' AND typed_value<>'""'::jsonb`); e != nil {
		return e
	}
	if _, e = s.db.Exec(ctx, `UPDATE configuration_security SET revision=(SELECT revision FROM configuration_head WHERE singleton=1),required_nodes='{}' WHERE singleton=1`); e != nil {
		return e
	}
	// Restored heartbeats describe the source deployment, not a live node in
	// the isolated target. Retain historical ACKs but require fresh liveness.
	_, e = s.db.Exec(ctx, `UPDATE configuration_nodes SET last_seen=LEAST(last_seen,now()-interval '31 seconds')`)
	return e
}
