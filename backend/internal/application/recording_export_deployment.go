package application

import "context"

// PauseRecordingExports is called only after the host executor has stopped and
// verified the sole backend worker. Conversion is a repeatable derivative: keep
// original recordings, completed cache entries and user cancellation decisions.
func (a *App) PauseRecordingExports(ctx context.Context) error {
	return a.db.Within(ctx, func(tx context.Context) error {
		if _, err := a.db.Exec(tx, `SELECT pg_advisory_xact_lock(110032)`); err != nil {
			return err
		}
		if _, err := a.db.Exec(tx, `UPDATE recording_exports e SET attempts=greatest(0,e.attempts-1),progress=0 FROM jobs j WHERE e.job_id=j.id AND j.kind='recording.mp4' AND NOT j.cancel_requested AND (j.state='running' OR (j.state='unknown' AND j.reason='worker_lease_lost'))`); err != nil {
			return err
		}
		_, err := a.db.Exec(tx, `UPDATE jobs SET state=CASE WHEN cancel_requested THEN 'cancelled' ELSE 'queued' END,reason=CASE WHEN cancel_requested THEN 'cancelled' ELSE 'component_paused' END,lease_token=NULL,lease_until=NULL,ended_at=CASE WHEN cancel_requested THEN now() ELSE NULL END WHERE kind='recording.mp4' AND (state IN ('queued','running') OR (state='unknown' AND reason='worker_lease_lost'))`)
		return err
	})
}
