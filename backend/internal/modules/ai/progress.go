package ai

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

// Progress is durable before the snapshot is sent to the browser.
func (s *Service) Progress(ctx context.Context, user string, t Turn, reply Reply) (Turn, error) {
	err := s.db.Row(ctx, `UPDATE ai_turns SET output=$3,revision=revision+1 WHERE id=$1 AND user_id=$2 AND state='running' AND NOT cancel_requested RETURNING revision`, t.ID, user, reply.Text).Scan(&t.Revision)
	t.Output = reply.Text
	return t, err
}
func (s *Service) CancelRequested(ctx context.Context, user, turn string) (bool, error) {
	var cancelled bool
	err := s.db.Row(ctx, `SELECT cancel_requested FROM ai_turns WHERE id=$1 AND user_id=$2`, turn, user).Scan(&cancelled)
	return cancelled, err
}
func (s *Service) Cancel(ctx context.Context, user, conversation, turn string) error {
	if _, err := s.Conversation(ctx, user, conversation); err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE ai_turns SET cancel_requested=true,revision=revision+1,state=CASE WHEN state='queued' THEN 'interrupted' ELSE state END,reason=CASE WHEN state='queued' THEN 'user_cancelled' ELSE reason END WHERE id=$1 AND user_id=$2 AND conversation_id=$3 AND state IN ('queued','running') AND deadline>now()`, turn, user, conversation)
	if err == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return err
}
