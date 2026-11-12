package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/platform/jobs"
	"os"
	"path/filepath"
	"time"
)

func (a *App) RunBackupMaintenance(ctx context.Context) {
	if a.config.BackupDirectory == "" || a.config.DatabaseURL == "" {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var nextRetention time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := a.tickBackupSchedule(ctx); e != nil && ctx.Err() == nil {
				slog.Error("backup scheduler failed")
			}
			if e := a.RunBackupOnce(ctx); e != nil && ctx.Err() == nil {
				slog.Error("backup worker failed")
			}
			if time.Now().After(nextRetention) {
				c, cancel := context.WithTimeout(ctx, 20*time.Second)
				if e := backup.New(a.db).Prune(c, a.config.BackupDirectory); e != nil && ctx.Err() == nil {
					slog.Error("backup retention failed")
				}
				if e := backup.New(a.db).PruneRemote(c, a.config.VaultKey); e != nil && ctx.Err() == nil {
					slog.Error("remote backup retention failed")
				}
				cancel()
				nextRetention = time.Now().Add(time.Hour)
			}
		}
	}
}
func (a *App) tickBackupSchedule(ctx context.Context) error {
	s := backup.New(a.db)
	cfg, e := s.Config(ctx)
	if e != nil || !cfg.ScheduleEnabled {
		return e
	}
	if e = backup.ValidateConfig(cfg); e != nil {
		return e
	}
	loc, e := time.LoadLocation(cfg.Timezone)
	if e != nil {
		return e
	}
	now := time.Now().In(loc)
	if now.Format("15:04") < cfg.ScheduleTime {
		return nil
	}
	sum := sha256.Sum256([]byte("daily-backup:" + cfg.Timezone + ":" + now.Format("2006-01-02")))
	key := fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	_, e = s.Request(ctx, "", key)
	return e
}
func (a *App) RunBackupOnce(parent context.Context) error {
	q := jobs.New(a.db)
	j, e := q.Claim(parent, "backup.create")
	if jobs.IsEmpty(e) {
		return nil
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Hour)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if q.Heartbeat(ctx, j) != nil {
					cancel()
					return
				}
			}
		}
	}()
	e = a.createBackup(ctx, j)
	cancel()
	<-done
	if e != nil {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		// No secrets, SQL or remote responses are copied to job errors.
		_ = backup.New(a.db).Stage(c, j, "failed")
		var localReady bool
		if qe := a.db.Row(c, `SELECT EXISTS(SELECT 1 FROM backup_files WHERE id=$1 AND state='available')`, j.ID).Scan(&localReady); qe != nil {
			return qe
		}
		state, reason := "failed", "backup_failed"
		if localReady {
			state = "partial"
			reason = "local_archive_available_remote_copy_incomplete"
		}
		if fe := q.Finish(c, j, state, reason); fe != nil {
			return fe
		}
	}
	return e
}
func (a *App) createBackup(ctx context.Context, j jobs.Job) error {
	if a.config.BackupDirectory == "" || a.config.DatabaseURL == "" {
		return errors.New("backup runtime not configured")
	}
	s := backup.New(a.db)
	task, e := s.Task(ctx, j.ID)
	if e != nil {
		return e
	}
	if e = backup.ValidateConfig(task.Config); e != nil {
		return e
	}
	dir := a.config.BackupDirectory
	if existing, e := s.LocalFile(ctx, j.ID); e == nil {
		if existing.StorageKey != j.ID+".ntbackup.age" {
			return errors.New("invalid local backup key")
		}
		file := filepath.Join(dir, existing.StorageKey)
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, f)
		f.Close()
		if e != nil || n != existing.Size || hex.EncodeToString(h.Sum(nil)) != existing.SHA256 {
			return errors.New("local retry archive changed")
		}
		if e = s.Stage(ctx, j, "uploading"); e != nil {
			return e
		}
		if e = s.CopyAll(ctx, j.ID, file, existing.SHA256, a.config.VaultKey, j); e != nil {
			return e
		}
		return a.finishBackup(ctx, j)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = s.Stage(ctx, j, "preparing"); e != nil {
		return e
	}
	drain, stop := context.WithTimeout(ctx, 60*time.Second)
	leave, e := a.beginMaintenance(drain)
	stop()
	if e != nil {
		return e
	}
	left := false
	defer func() {
		if !left {
			leave()
		}
	}()
	if e = s.Stage(ctx, j, "snapshotting"); e != nil {
		return e
	}
	key := j.ID + ".ntbackup.age"
	output := filepath.Join(dir, key)
	bundle := backup.RecoveryBundle{Version: 1, MFAKey: hex.EncodeToString(a.config.EncryptionKey), VaultKey: hex.EncodeToString(a.config.VaultKey)}
	manifest, e := backup.CreateSnapshot(ctx, a.config.DatabaseURL, dir, output, task.Config.Recipient, j.ID, Release, task.Config.Scope, bundle)
	if e != nil {
		return e
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(output)
		}
	}()
	if e = s.Stage(ctx, j, "verifying"); e != nil {
		return e
	}
	f, e := os.Open(output)
	if e != nil {
		return e
	}
	digest := sha256.New()
	size, e := io.Copy(digest, f)
	f.Close()
	if e != nil {
		return e
	}
	// A lost commit acknowledgement must never delete a potentially catalogued
	// archive. Preserve it for reconciliation if finalization becomes uncertain.
	published = true
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := s.Complete(c, j, backup.File{ID: j.ID, StorageKey: key, Size: size, SHA256: hex.EncodeToString(digest.Sum(nil)), Manifest: manifest}); e != nil {
			return e
		}
		return a.audit.Detail(c, "", "backup.local_ready", "backup:"+j.ID, "local", map[string]string{"id": j.ID, "scope": task.Config.Scope})
	})
	if e != nil {
		return e
	}
	leave()
	left = true
	if e = s.CopyAll(ctx, j.ID, output, hex.EncodeToString(digest.Sum(nil)), a.config.VaultKey, j); e != nil {
		return e
	}
	return a.finishBackup(ctx, j)
}
func (a *App) finishBackup(ctx context.Context, j jobs.Job) error {
	s := backup.New(a.db)
	return a.db.Within(ctx, func(c context.Context) error {
		if e := s.Stage(c, j, "succeeded"); e != nil {
			return e
		}
		if e := jobs.New(a.db).Finish(c, j, "succeeded", ""); e != nil {
			return e
		}
		return a.audit.Detail(c, "", "backup.completed", "backup:"+j.ID, "local", map[string]string{"id": j.ID})
	})
}
