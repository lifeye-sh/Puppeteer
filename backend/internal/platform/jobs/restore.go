package jobs

import "context"

func (q *Queue) QuarantineRestore(ctx context.Context) error {
	_, e := q.db.Exec(ctx, `UPDATE jobs SET state='unknown',reason='restored_requires_review',cancel_requested=true,lease_token=NULL,lease_until=NULL,ended_at=now() WHERE state IN ('queued','running')`)
	return e
}
