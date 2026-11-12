package session

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"time"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Session struct {
	Watermark         Watermark  `json:"watermark"`
	Forwarding        bool       `json:"forwarding"`
	ClientIP          string     `json:"-"`
	Protocol          string     `json:"protocol"`
	ID                string     `json:"id"`
	ActorID           string     `json:"actorId"`
	AssetID           string     `json:"assetId"`
	AssetVersion      int64      `json:"-"`
	CredentialVersion int64      `json:"-"`
	HostKeyVersion    int64      `json:"-"`
	Readonly          bool       `json:"readonly"`
	State             string     `json:"state"`
	Reason            string     `json:"reason"`
	CreatedAt         time.Time  `json:"createdAt"`
	ConnectedAt       *time.Time `json:"connectedAt"`
	ClosedAt          *time.Time `json:"closedAt"`
	LeaseExpiresAt    time.Time  `json:"leaseExpiresAt"`
	Terminate         bool       `json:"-"`
}

const cols = `id,actor_id,asset_id,asset_version,credential_version,host_key_version,readonly,state,close_reason,created_at,connected_at,closed_at,lease_expires_at,terminate_requested,protocol,client_ip,native_forward,watermark`

func (s *Service) Get(ctx context.Context, id string) (v Session, e error) {
	e = s.db.Row(ctx, `SELECT `+cols+` FROM sessions WHERE id=$1 AND audit_purged_at IS NULL`, id).Scan(&v.ID, &v.ActorID, &v.AssetID, &v.AssetVersion, &v.CredentialVersion, &v.HostKeyVersion, &v.Readonly, &v.State, &v.Reason, &v.CreatedAt, &v.ConnectedAt, &v.ClosedAt, &v.LeaseExpiresAt, &v.Terminate, &v.Protocol, &v.ClientIP, &v.Forwarding, &v.Watermark)
	return
}

// Reserve runs under the application's shared transaction, serializing global and per-user caps.
func (s *Service) Reserve(ctx context.Context, v Session, ticket, auth string) (string, error) {
	if !s.db.InTransaction(ctx) {
		var id string
		e := s.db.Within(ctx, func(c context.Context) error { var e error; id, e = s.Reserve(c, v, ticket, auth); return e })
		return id, e
	}
	if err := s.consumeAccessMFA(ctx, v.ActorID, auth); err != nil {
		return "", err
	}
	if v.Protocol == "" {
		v.Protocol = "ssh"
	}
	if e := s.db.Admit(ctx, "sessions", "session."+v.Protocol); e != nil {
		return "", e
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(904004)`); e != nil {
		return "", e
	}
	var total, own int
	if e := s.db.Row(ctx, `SELECT count(*),count(*) FILTER(WHERE actor_id=$1) FROM sessions WHERE closed_at IS NULL AND lease_expires_at>clock_timestamp() AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1)`, v.ActorID).Scan(&total, &own); e != nil {
		return "", e
	}
	if total >= 16 || own >= 4 {
		return "", fault.Conflict
	}
	e := s.db.Row(ctx, `INSERT INTO sessions(actor_id,asset_id,asset_version,credential_version,host_key_version,readonly,state,ticket_hash,auth_hash,ticket_expires_at,lease_expires_at,protocol,client_ip,quota_epoch) VALUES($1,$2,$3,$4,$5,$6,'reserved',$7,$8,clock_timestamp()+interval '30 seconds',clock_timestamp()+interval '30 seconds',$9,$10,(SELECT auth_epoch FROM instance_state WHERE singleton=1)) RETURNING id`, v.ActorID, v.AssetID, v.AssetVersion, v.CredentialVersion, v.HostKeyVersion, v.Readonly, ticket, auth, v.Protocol, v.ClientIP).Scan(&v.ID)
	if e == nil {
		e = s.captureWatermark(ctx, v.ID, v.ActorID, v.AssetID)
	}
	return v.ID, e
}
func (s *Service) Consume(ctx context.Context, id, actor, ticket, auth string) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.Consume(c, id, actor, ticket, auth) })
	}
	var protocol string
	if e := s.db.Row(ctx, `SELECT protocol FROM sessions WHERE id=$1`, id).Scan(&protocol); e != nil {
		return e
	}
	if e := s.db.Admit(ctx, "sessions.renew", "session."+protocol); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE sessions SET state='connecting',last_activity_at=clock_timestamp(),ticket_hash='',lease_expires_at=clock_timestamp()+interval '10 seconds' WHERE id=$1 AND actor_id=$2 AND ticket_hash=$3 AND auth_hash=$4 AND state='reserved' AND ticket_expires_at>clock_timestamp() AND lease_expires_at>clock_timestamp() AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1) AND NOT terminate_requested`, id, actor, ticket, auth)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return e
}
func (s *Service) Renew(ctx context.Context, id string) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.Renew(c, id) })
	}
	if e := s.checkIdle(ctx, id, false); e != nil {
		return e
	}
	var protocol string
	if e := s.db.Row(ctx, `SELECT protocol FROM sessions WHERE id=$1`, id).Scan(&protocol); e != nil {
		return e
	}
	if e := s.db.Admit(ctx, "sessions.renew", "session."+protocol); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE sessions SET lease_expires_at=clock_timestamp()+interval '10 seconds' WHERE id=$1 AND state IN ('connecting','connected') AND NOT terminate_requested AND lease_expires_at>clock_timestamp() AND created_at>now()-interval '4 hours' AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1)`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return e
}
func (s *Service) Connected(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE sessions SET state='connected',connected_at=now() WHERE id=$1 AND state='connecting' AND NOT terminate_requested AND lease_expires_at>clock_timestamp() AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1)`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return e
}
func (s *Service) Finish(ctx context.Context, id, state, reason string) error {
	_, e := s.db.Exec(ctx, `UPDATE sessions SET state=$2,close_reason=$3,closed_at=now(),ticket_hash='' WHERE id=$1 AND closed_at IS NULL`, id, state, reason)
	return e
}

// LockFinalization serializes live-handler and expired-lease finalizers in the shared transaction.
func (s *Service) LockFinalization(ctx context.Context, id string) (bool, error) {
	var closed bool
	e := s.db.Row(ctx, `SELECT closed_at IS NOT NULL FROM sessions WHERE id=$1 FOR UPDATE`, id).Scan(&closed)
	return closed, e
}
func (s *Service) Terminate(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE sessions SET terminate_requested=true WHERE id=$1 AND closed_at IS NULL`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) Expired(ctx context.Context) ([]string, error) {
	rows, e := s.db.Rows(ctx, `SELECT id FROM sessions WHERE closed_at IS NULL AND (lease_expires_at<=clock_timestamp() OR quota_epoch<>(SELECT auth_epoch FROM instance_state WHERE singleton=1) OR (state='reserved' AND terminate_requested))`)
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
func (s *Service) List(ctx context.Context, actor string, online bool, offset, limit int) ([]Session, int, error) {
	where := ` FROM sessions WHERE audit_purged_at IS NULL AND ($1='' OR actor_id=NULLIF($1,'')::uuid) AND (NOT $2 OR (closed_at IS NULL AND lease_expires_at>clock_timestamp()))`
	var total int
	if e := s.db.Row(ctx, `SELECT count(*)`+where, actor, online).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT `+cols+where+` ORDER BY created_at DESC,id LIMIT $3 OFFSET $4`, actor, online, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var v Session
		if e = rows.Scan(&v.ID, &v.ActorID, &v.AssetID, &v.AssetVersion, &v.CredentialVersion, &v.HostKeyVersion, &v.Readonly, &v.State, &v.Reason, &v.CreatedAt, &v.ConnectedAt, &v.ClosedAt, &v.LeaseExpiresAt, &v.Terminate, &v.Protocol, &v.ClientIP, &v.Forwarding, &v.Watermark); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
