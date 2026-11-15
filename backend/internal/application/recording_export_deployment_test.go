package application

import (
	"context"
	"testing"
)

func TestMP4DeploymentPausePreservesTerminalStatesAndCancellations(t *testing.T) {
	_, a, _ := r02Server(t, false)
	ctx := context.Background()
	for _, tc := range []struct {
		kind, state, reason string
		cancelled           bool
		want                string
	}{
		{"recording.mp4", "queued", "", false, "queued"},
		{"recording.mp4", "running", "", false, "queued"},
		{"recording.mp4", "unknown", "worker_lease_lost", false, "queued"},
		{"recording.mp4", "unknown", "other", false, "unknown"},
		{"recording.mp4", "running", "", true, "cancelled"},
		{"recording.mp4", "queued", "", true, "cancelled"},
		{"recording.mp4", "succeeded", "", false, "succeeded"},
		{"recording.mp4", "failed", "conversion_failed", false, "failed"},
		{"recording.mp4", "cancelled", "cancelled", true, "cancelled"},
		{"ssh.command", "running", "", false, "running"},
	} {
		var id string
		if err := a.db.Row(ctx, `INSERT INTO jobs(kind,state,reason,cancel_requested,lease_token,lease_until) VALUES($1,$2,$3,$4,gen_random_uuid(),now()+interval '1 hour') RETURNING id`, tc.kind, tc.state, tc.reason, tc.cancelled).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := a.PauseRecordingExports(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.PauseRecordingExports(ctx); err != nil {
			t.Fatal(err)
		}
		var state string
		var cancelled bool
		if err := a.db.Row(ctx, `SELECT state,cancel_requested FROM jobs WHERE id=$1`, id).Scan(&state, &cancelled); err != nil || state != tc.want || cancelled != tc.cancelled {
			t.Fatal(tc, state, cancelled, err)
		}
	}
}
