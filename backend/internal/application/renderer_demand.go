package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/jobs"
	"puppeteer/backend/internal/platform/resourcebudget"
	"strings"
	"time"
)

func notifyWork(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (a *App) rendererRuntimeCall(ctx context.Context, operation string) error {
	token, err := os.ReadFile(a.config.DeployTokenFile)
	if err != nil {
		return errors.New("renderer_executor_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	status, _, err := (deploytask.Client{Socket: a.config.DeploySocket, Token: strings.TrimSpace(string(token))}).Call(ctx, "POST", "/v1/renderer/"+operation, nil)
	if err != nil {
		return errors.New("renderer_executor_unavailable")
	}
	if status == 409 {
		return errors.New("deployment_busy")
	}
	if status == 404 || status == 501 {
		return errors.New("executor_upgrade_required")
	}
	if status != 200 {
		return errors.New("renderer_start_failed")
	}
	return nil
}

func (a *App) claimRecordingExport(ctx context.Context) (jobs.Job, error) {
	return a.claimRecordingExportWhen(ctx, func() error { return resourcebudget.ExportPressure(ctx, a.config.MP4Directory) })
}

func (a *App) claimRecordingExportWhen(ctx context.Context, admission func() error) (jobs.Job, error) {
	var pending bool
	if err := a.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE kind='recording.mp4' AND state='queued' AND NOT cancel_requested)`).Scan(&pending); err != nil {
		return jobs.Job{}, err
	}
	if !pending {
		return jobs.Job{}, pgx.ErrNoRows
	}
	if err := admission(); err != nil {
		_, updateErr := a.db.Exec(ctx, `UPDATE jobs SET reason=$1 WHERE kind='recording.mp4' AND state='queued' AND NOT cancel_requested AND reason IS DISTINCT FROM $1`, err.Error())
		if updateErr != nil {
			return jobs.Job{}, updateErr
		}
		return jobs.Job{}, pgx.ErrNoRows // Deferred admission is not a failed attempt.
	}
	if a.config.MP4OnDemand {
		_, _ = a.db.Exec(ctx, `UPDATE jobs SET reason='renderer_starting' WHERE kind='recording.mp4' AND state='queued' AND NOT cancel_requested`)
		if err := a.rendererRuntimeCall(ctx, "wake"); err != nil {
			_, _ = a.db.Exec(ctx, `UPDATE jobs SET reason=$1 WHERE kind='recording.mp4' AND state='queued' AND NOT cancel_requested`, err.Error())
			return jobs.Job{}, err
		}
	}
	if a.config.MP4URL != "" {
		if err := a.rendererAdmission(ctx); err != nil {
			_, updateErr := a.db.Exec(ctx, `UPDATE jobs SET reason=$1 WHERE kind='recording.mp4' AND state='queued' AND NOT cancel_requested AND reason IS DISTINCT FROM $1`, err.Error())
			if updateErr != nil {
				return jobs.Job{}, updateErr
			}
			return jobs.Job{}, pgx.ErrNoRows
		}
	}
	var claimed jobs.Job
	err := a.db.Within(ctx, func(tx context.Context) error {
		var e error
		claimed, e = jobs.New(a.db).Claim(tx, "recording.mp4")
		if e != nil {
			return e
		}
		_, e = a.db.Exec(tx, `UPDATE jobs SET reason='' WHERE id=$1`, claimed.ID)
		return e
	})
	return claimed, err
}

// The readiness probe is optional for older renderer images. Capacity is checked
// again on POST /jobs, so a successful probe is never treated as a reservation.
func (a *App) rendererAdmission(ctx context.Context) error {
	response, err := a.rendererRequest(ctx, "GET", "/runtime/admission", nil)
	if err != nil {
		return errors.New("converter_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return nil
	}
	if response.StatusCode != 200 {
		return rendererResponseError(response)
	}
	return nil
}
