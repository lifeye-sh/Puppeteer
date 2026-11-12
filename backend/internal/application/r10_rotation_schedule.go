package application

import (
	"context"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
)

func (a *App) prepareScheduledRotation(ctx context.Context, job jobs.Job) error {
	s := automation.New(a.db)
	run, e := s.Run(ctx, job.ID)
	if e != nil {
		return e
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(a.db).Fence(c, job); e != nil {
			return e
		}
		if e := s.DefinitionActive(c, run.ScheduleID); e != nil {
			return e
		}
		if e := a.vault.AttachScheduledRotation(c, job.ID, run.ActorID, run.Snapshot.SourceIP); e != nil {
			return e
		}
		for _, id := range run.Snapshot.AssetIDs {
			asset, u, e := a.taskAsset(c, run.ActorID, id, run.Snapshot.SourceIP, "credential.rotate")
			if e != nil {
				return e
			}
			if e = a.allowed(c, u, "credential.rotate"); e != nil {
				return e
			}
			cred, e := a.vault.Get(c, asset.CredentialID)
			if e != nil {
				return e
			}
			trust, e := a.connectivity.Get(c, id)
			if e != nil {
				return e
			}
			if trust.Host != asset.Host || trust.Port != asset.Port {
				return fault.Conflict
			}
			_, e = a.vault.AddRotationTarget(c, vault.RotationTarget{RunID: job.ID, AssetID: id, AssetVersion: asset.Version, CredentialID: cred.ID, CredentialVersion: cred.Version, AccountKey: trust.Fingerprint + ":" + strings.ToLower(cred.Username), Kind: cred.Kind})
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e == nil {
		return nil
	}
	// No target was dispatched; the failed planning transaction rolled back all
	// candidates. Record an explicit failure rather than retrying the mutation.
	record := a.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(a.db).Owned(c, job); e != nil {
			return e
		}
		for _, id := range run.Snapshot.AssetIDs {
			if e := s.TargetDone(c, job.ID, automation.Target{AssetID: id, State: "failed", Reason: "rotation_preflight_failed"}); e != nil {
				return e
			}
		}
		return jobs.New(a.db).Finish(c, job, "failed", "rotation_preflight_failed")
	})
	if record != nil {
		return record
	}
	return fault.Conflict
}
