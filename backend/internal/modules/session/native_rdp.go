package session

import (
	"context"
	"encoding/hex"
	"next-terminal/backend/internal/platform/fault"
)

// IssueNativeRDPTicket belongs in the same application transaction as Reserve,
// current authorization and audit. Only a purpose-bound HMAC digest is stored.
// Removing the browser ticket separates the native and web entry points.
func (s *Service) IssueNativeRDPTicket(ctx context.Context, id, digest, family string) error {
	if !s.db.InTransaction(ctx) || !rdpDigest(digest) {
		return fault.Invalid
	}
	if e := s.db.Admit(ctx, "sessions.renew", "session.rdp"); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `INSERT INTO native_rdp_tickets(session_id,ticket_hash,family_id,expires_at)
 SELECT s.id,$2,f.id,LEAST(clock_timestamp()+interval '5 minutes',f.expires_at) FROM sessions s JOIN oauth_token_families f ON f.id=$3 AND f.user_id=s.actor_id AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp()
 WHERE s.id=$1 AND protocol='rdp' AND state='reserved' AND closed_at IS NULL AND NOT terminate_requested AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1) AND lease_expires_at>clock_timestamp()`, id, digest, family)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Denied
	}
	_, e = s.db.Exec(ctx, `UPDATE sessions SET ticket_hash='',ticket_expires_at=clock_timestamp()+interval '5 minutes',lease_expires_at=clock_timestamp()+interval '5 minutes' WHERE id=$1`, id)
	return e
}

// ConsumeNativeRDPTicket requires the application's authorization transaction.
// Commit consumption before requesting target credentials or dialing; a target
// failure must never roll this transaction back or make the ticket reusable.
func (s *Service) ConsumeNativeRDPTicket(ctx context.Context, id, actor, digest, worker string) (Session, error) {
	var empty Session
	if !s.db.InTransaction(ctx) || !rdpDigest(digest) {
		return empty, fault.Invalid
	}
	if e := s.db.Admit(ctx, "sessions.renew", "session.rdp"); e != nil {
		return empty, e
	}
	var allowed bool
	e := s.db.Row(ctx, `SELECT actor_id=$2 AND protocol='rdp' AND state='reserved' AND closed_at IS NULL AND NOT terminate_requested AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1) AND lease_expires_at>clock_timestamp() FROM sessions WHERE id=$1 FOR UPDATE`, id, actor).Scan(&allowed)
	if e != nil || !allowed {
		return empty, fault.Denied
	}
	tag, e := s.db.Exec(ctx, `UPDATE native_rdp_tickets SET consumed_at=clock_timestamp(),worker_id=$3 WHERE session_id=$1 AND ticket_hash=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp() AND EXISTS(SELECT 1 FROM oauth_token_families f WHERE f.id=native_rdp_tickets.family_id AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp())`, id, digest, worker)
	if e != nil {
		return empty, e
	}
	if tag.RowsAffected() != 1 {
		return empty, fault.Denied
	}
	_, e = s.db.Exec(ctx, `UPDATE sessions SET state='connecting',last_activity_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '10 seconds' WHERE id=$1`, id)
	if e != nil {
		return empty, e
	}
	return s.Get(ctx, id)
}

func rdpDigest(v string) bool {
	if len(v) != 64 {
		return false
	}
	b, e := hex.DecodeString(v)
	return e == nil && hex.EncodeToString(b) == v
}
