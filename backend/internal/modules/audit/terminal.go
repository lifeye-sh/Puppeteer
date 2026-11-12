package audit

import (
	"context"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

type Access struct {
	SessionID       string     `json:"sessionId"`
	Username        string     `json:"username"`
	AssetName       string     `json:"assetName"`
	Target          string     `json:"target"`
	State           string     `json:"state"`
	Reason          string     `json:"reason"`
	CreatedAt       time.Time  `json:"createdAt"`
	EndedAt         *time.Time `json:"endedAt"`
	RecordingState  string     `json:"recordingState"`
	RecordingBytes  int64      `json:"recordingBytes"`
	RecordingEvents int64      `json:"recordingEvents"`
	RecordingFormat string     `json:"recordingFormat"`
}

func (s *Service) OpenAccess(ctx context.Context, id, actor, asset, username, name, target, ip string) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.OpenAccess(c, id, actor, asset, username, name, target, ip) })
	}
	var revision int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&revision); e != nil {
		return e
	}
	config, e := system.New(s.db).SecurityConfiguration(ctx)
	if e != nil {
		return e
	}

	if _, e := s.db.Exec(ctx, `INSERT INTO access_logs(session_id,actor_id,asset_id,username,asset_name,target,source_ip,state) VALUES($1,$2,$3,$4,$5,$6,$7,'reserved')`, id, actor, asset, username, name, target, ip); e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO recordings(session_id,enabled) VALUES($1,$2)`, id, config.Values.Bool("recording-enabled"))
	return e
}
func (s *Service) AccessState(ctx context.Context, id, state, reason string, closed bool) error {
	if _, e := s.db.Exec(ctx, `UPDATE access_logs SET state=$2,reason=$3,ended_at=CASE WHEN $4 THEN now() ELSE NULL END WHERE session_id=$1`, id, state, reason, closed); e != nil {
		return e
	}
	if closed {
		_, e := s.db.Exec(ctx, `UPDATE recordings SET state=CASE WHEN $2 IN ('closed','failed') THEN 'complete' ELSE 'interrupted' END,ended_at=now() WHERE session_id=$1`, id, state)
		if e != nil {
			return e
		}
		_, e = s.db.Exec(ctx, `UPDATE session_commands SET state='interrupted',ended_at=now() WHERE session_id=$1 AND state='running'`, id)
		return e
	}
	return nil
}
func (s *Service) Access(ctx context.Context, id string) (v Access, e error) {
	e = s.db.Row(ctx, `SELECT a.session_id,username,asset_name,target,a.state,reason,a.created_at,a.ended_at,CASE WHEN r.state='expired' THEN 'expired' WHEN r.enabled THEN r.state ELSE 'disabled' END,r.bytes,r.events,r.format FROM access_logs a JOIN recordings r USING(session_id) WHERE a.session_id=$1`, id).Scan(&v.SessionID, &v.Username, &v.AssetName, &v.Target, &v.State, &v.Reason, &v.CreatedAt, &v.EndedAt, &v.RecordingState, &v.RecordingBytes, &v.RecordingEvents, &v.RecordingFormat)
	return
}

// Persist BEFORE forwarding output; quota exhaustion or storage failure closes the SSH transport.
func (s *Service) Record(ctx context.Context, id, kind string, elapsed int64, payload []byte) error {
	return s.db.Within(ctx, func(ctx context.Context) error {
		var sequence int64
		var enabled bool
		var state string
		if e := s.db.Row(ctx, `SELECT enabled,state FROM recordings WHERE session_id=$1`, id).Scan(&enabled, &state); e != nil {
			return e
		}
		if state != "writing" {
			return fault.Conflict
		}
		if !enabled {
			return nil
		}
		e := s.db.Row(ctx, `UPDATE recordings SET bytes=bytes+$2,events=events+1 WHERE session_id=$1 AND state='writing' AND bytes+$2<=67108864 RETURNING events`, id, len(payload)).Scan(&sequence)
		if e != nil {
			return e
		}
		_, e = s.db.Exec(ctx, `INSERT INTO recording_chunks(session_id,sequence,elapsed_ms,kind,payload) VALUES($1,$2,$3,$4,$5)`, id, sequence, elapsed, kind, payload)
		return e
	})
}

type Command struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Command   string    `json:"command"`
	Source    string    `json:"source"`
	Note      string    `json:"note"`
	State     string    `json:"state"`
	ExitCode  *int      `json:"exitCode"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Service) StartCommand(ctx context.Context, id, actor, command string) (string, error) {
	var cid string
	e := s.db.Within(ctx, func(ctx context.Context) error {
		var e error
		cid, e = s.StartCommandInTransaction(ctx, id, actor, command)
		return e
	})
	return cid, e
}

// StartCommandInTransaction shares the caller's authorization/session transaction.
// Do not nest DB.Within: it starts an independent transaction and can deadlock on
// the caller's user/session foreign-key locks.
func (s *Service) StartCommandInTransaction(ctx context.Context, id, actor, command string) (string, error) {
	if e := s.reserveCommands(ctx, id, 1); e != nil {
		return "", e
	}
	var cid string
	e := s.db.Row(ctx, `INSERT INTO session_commands(session_id,actor_id,command) VALUES($1,$2,$3) RETURNING id`, id, actor, command).Scan(&cid)
	return cid, e
}

// Bound command storage for the entire session. Failure must precede forwarding input.
func (s *Service) reserveCommands(ctx context.Context, id string, n int) error {
	tag, e := s.db.Exec(ctx, `UPDATE recordings SET command_count=command_count+$2 WHERE session_id=$1 AND state='writing' AND command_count+$2<=10000`, id, n)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) Submissions(ctx context.Context, id, actor string, items []*Submission) error {
	if len(items) == 0 {
		return nil
	}
	return s.db.Within(ctx, func(ctx context.Context) error {
		if e := s.reserveCommands(ctx, id, len(items)); e != nil {
			return e
		}
		for _, v := range items {
			if e := s.db.Row(ctx, `INSERT INTO session_commands(session_id,actor_id,command,source,state,note,ended_at) VALUES($1,$2,'','terminal-input','unrecognized',$3,now()) RETURNING id`, id, actor, v.Note).Scan(&v.ID); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Service) ResolveSubmission(ctx context.Context, v *Submission) error {
	tag, e := s.db.Exec(ctx, `UPDATE session_commands SET command=$2,note=$3,state='observed' WHERE id=$1 AND source='terminal-input' AND state='unrecognized'`, v.ID, v.Command, v.Note)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) EndCommand(ctx context.Context, id, state string, code int) error {
	tag, e := s.db.Exec(ctx, `UPDATE session_commands SET state=$2,exit_code=$3,ended_at=now() WHERE id=$1 AND state='running'`, id, state, code)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
func (s *Service) Commands(ctx context.Context, id string) ([]Command, error) {
	return s.commandRows(ctx, id, 0, 100)
}
func (s *Service) CommandPage(ctx context.Context, id string, offset, limit int) ([]Command, int, error) {
	var total int
	if e := s.db.Row(ctx, `SELECT count(*) FROM session_commands WHERE session_id=$1`, id).Scan(&total); e != nil {
		return nil, 0, e
	}
	items, e := s.commandRows(ctx, id, offset, limit)
	return items, total, e
}
func (s *Service) commandRows(ctx context.Context, id string, offset, limit int) ([]Command, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,session_id,command,source,note,state,exit_code,created_at FROM session_commands WHERE session_id=$1 ORDER BY created_at DESC,sequence DESC OFFSET $2 LIMIT $3`, id, offset, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		var v Command
		if e = rows.Scan(&v.ID, &v.SessionID, &v.Command, &v.Source, &v.Note, &v.State, &v.ExitCode, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Immutable per-session decision, captured before accepting remote traffic.
func (s *Service) RecordingEnabled(ctx context.Context, id string) (bool, error) {
	var enabled bool
	e := s.db.Row(ctx, `SELECT enabled FROM recordings WHERE session_id=$1`, id).Scan(&enabled)
	return enabled, e
}
