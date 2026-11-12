package session

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type Share struct {
	ID          string     `json:"id"`
	RecipientID string     `json:"recipientId"`
	Username    string     `json:"username"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	ClaimedAt   *time.Time `json:"claimedAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
}

func (s *Service) Shares(ctx context.Context, id string) ([]Share, error) {
	rows, e := s.db.Rows(ctx, `SELECT s.id,s.recipient_id,u.username,s.expires_at,s.claimed_at,s.revoked_at FROM session_shares s JOIN users u ON u.id=s.recipient_id WHERE session_id=$1 ORDER BY s.created_at DESC LIMIT 64`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var v Share
		if e = rows.Scan(&v.ID, &v.RecipientID, &v.Username, &v.ExpiresAt, &v.ClaimedAt, &v.RevokedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) CreateShare(ctx context.Context, id, creator, recipient, hash string, minutes int) (Share, error) {
	var v Share
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(904019)`); e != nil {
		return v, e
	}
	var count int
	if e := s.db.Row(ctx, `SELECT count(*) FROM session_shares WHERE session_id=$1`, id).Scan(&count); e != nil {
		return v, e
	}
	if count >= 64 {
		return v, fault.Conflict
	}
	e := s.db.Row(ctx, `INSERT INTO session_shares(session_id,creator_id,recipient_id,token_hash,expires_at) VALUES($1,$2,$3,$4,now()+$5*interval '1 minute') RETURNING id,recipient_id,expires_at`, id, creator, recipient, hash, minutes).Scan(&v.ID, &v.RecipientID, &v.ExpiresAt)
	return v, e
}
func (s *Service) RevokeShare(ctx context.Context, id, share string) error {
	tag, e := s.db.Exec(ctx, `UPDATE session_shares SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND session_id=$2`, share, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return e
}
func (s *Service) Observe(ctx context.Context, id, actor, auth, ticket string) (string, error) {
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(904019)`); e != nil {
		return "", e
	}
	var total, count int
	if e := s.db.Row(ctx, `SELECT count(*),count(*) FILTER(WHERE expires_at>now() AND heartbeat_expires_at>now()) FROM session_observers WHERE session_id=$1`, id).Scan(&total, &count); e != nil {
		return "", e
	}
	if count >= 8 || total >= 256 {
		return "", fault.Conflict
	}
	share := ""
	expires := time.Now().Add(30 * time.Minute)
	if ticket != "" {
		if e := s.db.Row(ctx, `UPDATE session_shares SET claimed_at=now() WHERE session_id=$1 AND recipient_id=$2 AND token_hash=$3 AND claimed_at IS NULL AND revoked_at IS NULL AND expires_at>now() RETURNING id,expires_at`, id, actor, ticket).Scan(&share, &expires); e != nil {
			return "", fault.Denied
		}
	}
	var observer string
	e := s.db.Row(ctx, `INSERT INTO session_observers(session_id,actor_id,auth_hash,share_id,expires_at) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5) RETURNING id`, id, actor, auth, share, expires).Scan(&observer)
	return observer, e
}
func (s *Service) Observer(ctx context.Context, id, observer, actor, auth string) (bool, error) {
	var shared bool
	e := s.db.Row(ctx, `UPDATE session_observers o SET heartbeat_expires_at=now()+interval '30 seconds' WHERE o.id=$1 AND o.session_id=$2 AND o.actor_id=$3 AND o.auth_hash=$4 AND o.expires_at>now() AND o.heartbeat_expires_at>now() AND (o.share_id IS NULL OR EXISTS(SELECT 1 FROM session_shares sh WHERE sh.id=o.share_id AND sh.revoked_at IS NULL AND sh.expires_at>now())) RETURNING o.share_id IS NOT NULL`, observer, id, actor, auth).Scan(&shared)
	if e != nil {
		return false, fault.Denied
	}
	return shared, nil
}
func (s *Service) CloseObserver(ctx context.Context, id, observer, actor, auth string) error {
	_, e := s.db.Exec(ctx, `UPDATE session_observers SET expires_at=now() WHERE id=$1 AND session_id=$2 AND actor_id=$3 AND auth_hash=$4`, observer, id, actor, auth)
	return e
}
