package ai

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

// Branch copies a completed prefix, never executions, approvals, or usage rows.
// It therefore cannot replay a historical tool or reset the hourly usage ledger.
func (s *Service) Branch(ctx context.Context, user, id, through, title string) (string, error) {
	c, e := s.Conversation(ctx, user, id)
	if e != nil {
		return "", e
	}
	turns, e := s.Turns(ctx, user, id)
	if e != nil {
		return "", e
	}
	index := -1
	for i, t := range turns {
		if t.ID == through {
			index = i
		}
		if t.State == "running" || t.State == "queued" {
			return "", fault.Conflict
		}
	}
	if index < 0 {
		return "", fault.Invalid
	}
	if title == "" {
		title = "分支 · " + c.Title
		if len([]rune(title)) > 100 {
			title = string([]rune(title)[:100])
		}
	}
	next, e := s.Create(ctx, user, c.ProfileID, title, c)
	if e != nil {
		return "", e
	}
	if _, e = s.db.Exec(ctx, `UPDATE ai_conversations SET parent_id=$2 WHERE id=$1`, next, id); e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO ai_turns(conversation_id,user_id,client_message_id,prompt,output,state,reason,model,created_at,deadline,revision) SELECT $1,user_id,gen_random_uuid(),prompt,output,state,reason,model,created_at,now(),revision FROM ai_turns WHERE conversation_id=$2 AND (created_at,id)<=(SELECT created_at,id FROM ai_turns WHERE id=$3) ORDER BY created_at,id`, next, id, through)
	return next, e
}

// A reviewed summary replaces only the model's historical context, not audit or
// visible messages. A cutoff ID prevents silently compressing a concurrent turn.
func (s *Service) Compact(ctx context.Context, user, id, through, summary string) error {
	if len(summary) > 16384 || strings.TrimSpace(summary) == "" {
		return fault.Invalid
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,151))`, user); e != nil {
		return e
	}
	if _, e := s.Conversation(ctx, user, id); e != nil {
		return e
	}
	var active bool
	var latest string
	var at time.Time
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM ai_turns WHERE conversation_id=$1 AND state IN ('queued','running') AND deadline>now())`, id).Scan(&active); e != nil {
		return e
	}
	if active {
		return fault.Conflict
	}
	if e := s.db.Row(ctx, `SELECT id,created_at FROM ai_turns WHERE conversation_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, id).Scan(&latest, &at); e != nil {
		return e
	}
	if latest != through {
		return fault.Conflict
	}
	_, e := s.db.Exec(ctx, `UPDATE ai_conversations SET summary=$3,compacted_before=$4,updated_at=now() WHERE id=$1 AND user_id=$2`, id, user, summary, at)
	return e
}
