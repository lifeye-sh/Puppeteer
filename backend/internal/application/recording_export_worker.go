package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/jobs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var mp4HTTP = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (a *App) rendererRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.config.MP4URL, "/")+path, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+a.config.MP4Token)
	r.Header.Set("Content-Type", "application/x-ndjson")
	return mp4HTTP.Do(r)
}
func (a *App) rendererJSON(ctx context.Context, method, path string, body io.Reader, dst any) error {
	r, e := a.rendererRequest(ctx, method, path, body)
	if e != nil {
		return errors.New("converter_unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return errors.New("converter_unavailable")
	}
	if dst != nil {
		return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
	}
	return nil
}
func (a *App) RunRecordingExports(ctx context.Context) {
	if !a.mp4Available() {
		return
	}
	if e := os.MkdirAll(a.config.MP4Directory, 0700); e != nil {
		slog.Error("MP4 cache unavailable")
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var cleanup time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			// Conversion is a pure derivative, so a lost worker can restart safely.
			_, e := a.db.Exec(ctx, `UPDATE jobs j SET state=CASE WHEN e.attempts<3 THEN 'queued' ELSE 'failed' END,reason=CASE WHEN e.attempts<3 THEN 'restarting' ELSE 'worker_lease_lost' END,lease_token=NULL,lease_until=NULL FROM recording_exports e WHERE e.job_id=j.id AND NOT j.cancel_requested AND ((j.state='running' AND j.lease_until<now()) OR (j.state='unknown' AND j.reason='worker_lease_lost'))`)
			if e != nil {
				continue
			}
			_, _ = a.db.Exec(ctx, `UPDATE jobs SET state='cancelled',ended_at=now() WHERE kind='recording.mp4' AND state='queued' AND cancel_requested`)
			j, e := jobs.New(a.db).Claim(ctx, "recording.mp4")
			if e == nil {
				a.runRecordingExport(ctx, j)
			} else if !jobs.IsEmpty(e) && ctx.Err() == nil {
				slog.Warn("MP4 queue unavailable")
			}
			if time.Now().After(cleanup) {
				a.pruneRecordingExports(ctx)
				cleanup = time.Now().Add(time.Minute)
			}
		}
	}
}
func (a *App) runRecordingExport(parent context.Context, j jobs.Job) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Hour)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if jobs.New(a.db).Heartbeat(ctx, j) != nil {
					cancel()
					return
				}
			}
		}
	}()
	e := a.convertRecording(ctx, j)
	cancel()
	<-done
	if e == nil {
		return
	}
	c, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	var cancelled bool
	_ = a.db.Row(c, `SELECT cancel_requested FROM jobs WHERE id=$1`, j.ID).Scan(&cancelled)
	if parent.Err() != nil && !cancelled {
		// A maintenance drain or graceful restart can retry without consuming attempts.
		_ = a.db.Within(c, func(tx context.Context) error {
			if e := jobs.New(a.db).Owned(tx, j); e != nil {
				return e
			}
			if _, e := a.db.Exec(tx, `UPDATE jobs SET state='queued',reason='restarting',lease_token=NULL,lease_until=NULL WHERE id=$1`, j.ID); e != nil {
				return e
			}
			_, e := a.db.Exec(tx, `UPDATE recording_exports SET attempts=greatest(0,attempts-1),progress=0 WHERE job_id=$1`, j.ID)
			return e
		})
		return
	}
	state, reason := "failed", "conversion_failed"
	if cancelled {
		state, reason = "cancelled", "cancelled"
	} else {
		switch e.Error() {
		case "converter_unavailable", "recording_integrity_failed", "recording_limit", "output_limit":
			reason = e.Error()
		}
	}
	_ = jobs.New(a.db).Finish(c, j, state, reason)
}
func (a *App) convertRecording(ctx context.Context, j jobs.Job) error {
	var sessionID string
	if e := a.db.Row(ctx, `UPDATE recording_exports SET attempts=attempts+1,progress=0,updated_at=now() WHERE job_id=$1 RETURNING session_id::text`, j.ID).Scan(&sessionID); e != nil {
		return e
	}
	if e := backup.RequireSpace(ctx, a.config.MP4Directory, 512<<20); e != nil {
		return e
	}
	input, e := os.CreateTemp(a.config.MP4Directory, j.ID+"-*.jsonl")
	if e != nil {
		return e
	}
	defer os.Remove(input.Name())
	defer input.Close()
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.audit.VerifyRecordingLocked(c, sessionID); e != nil {
			return errors.New("recording_integrity_failed")
		}
		page, e := a.audit.Recording(c, sessionID, 0)
		if e != nil {
			return e
		}
		if page.State == "writing" || page.Total == 0 || page.Bytes > 100<<20 || page.DurationMS > 7200000 {
			return errors.New("recording_limit")
		}
		enc := json.NewEncoder(input)
		if e = enc.Encode(map[string]any{"type": "header", "format": page.Format, "durationMs": page.DurationMS, "events": page.Total}); e != nil {
			return e
		}
		var after int64
		if page.Format == audit.MediaFormat {
			for _, segment := range page.Segments {
				data, err := a.audit.MediaBytes(c, sessionID, segment.Sequence)
				if err != nil {
					return errors.New("recording_integrity_failed")
				}
				if err = enc.Encode(audit.RecordingEvent{Sequence: segment.Sequence, ElapsedMS: segment.StartMS + segment.DurationMS, Kind: "mp4", Payload: data}); err != nil {
					return err
				}
			}
			return enc.Encode(map[string]any{"type": "end", "events": page.Total})
		}
		for after < page.Total {
			page, e = a.audit.Recording(c, sessionID, after)
			if e != nil {
				return e
			}
			if page.Next <= after {
				return errors.New("recording_integrity_failed")
			}
			for _, event := range page.Events {
				if e = enc.Encode(event); e != nil {
					return e
				}
			}
			after = page.Next
		}
		return enc.Encode(map[string]any{"type": "end", "events": page.Total})
	})
	if e != nil {
		return e
	}
	if _, e = input.Seek(0, 0); e != nil {
		return e
	}
	var remote struct {
		ID string `json:"id"`
	}
	if e = a.rendererJSON(ctx, "POST", "/jobs", input, &remote); e != nil {
		return e
	}
	if !resource.ValidID(remote.ID) {
		return errors.New("invalid converter response")
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_ = a.rendererJSON(c, "DELETE", "/jobs/"+remote.ID, nil, nil)
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var status struct {
			State    string `json:"state"`
			Progress int    `json:"progress"`
			Reason   string `json:"reason"`
		}
		if e = a.rendererJSON(ctx, "GET", "/jobs/"+remote.ID, nil, &status); e != nil {
			return e
		}
		if status.State == "failed" {
			if status.Reason == "output_limit" {
				return errors.New("output_limit")
			}
			return errors.New("conversion_failed")
		}
		if e = a.audit.ExportProgress(ctx, j, max(0, min(99, status.Progress))); e != nil {
			return e
		}
		if status.State == "succeeded" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	response, e := a.rendererRequest(ctx, "GET", "/jobs/"+remote.ID+"/result", nil)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("conversion_failed")
	}
	output, e := os.CreateTemp(a.config.MP4Directory, j.ID+"-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(output.Name())
	defer output.Close()
	hash := sha256.New()
	size, e := io.Copy(io.MultiWriter(output, hash), io.LimitReader(response.Body, (256<<20)+1))
	if e != nil {
		return e
	}
	if size > 256<<20 {
		return errors.New("output_limit")
	}
	if size < 32 {
		return errors.New("conversion_failed")
	}
	if _, e = output.Seek(4, 0); e != nil {
		return e
	}
	var magic [4]byte
	if _, e = io.ReadFull(output, magic[:]); e != nil || string(magic[:]) != "ftyp" {
		return errors.New("conversion_failed")
	}
	if e = output.Sync(); e != nil {
		return e
	}
	if e = output.Close(); e != nil {
		return e
	}
	return a.db.Within(ctx, func(c context.Context) error {
		if e := jobs.New(a.db).Fence(c, j); e != nil {
			return e
		}
		if e := os.Rename(output.Name(), filepath.Join(a.config.MP4Directory, j.ID+".mp4")); e != nil {
			return e
		}
		if e := a.audit.CompleteExport(c, j, size, hex.EncodeToString(hash.Sum(nil))); e != nil {
			return e
		}
		return a.audit.Detail(c, "", "recording.mp4_completed", "mp4:"+j.ID, "local", map[string]string{"sessionId": sessionID})
	})
}

// Derived files expire after seven days, or when their source access record is
// purged. The raw recording remains authoritative and can be converted again.
func (a *App) pruneRecordingExports(ctx context.Context) {
	_ = a.db.Within(ctx, func(c context.Context) error {
		rows, e := a.db.Rows(c, `SELECT e.job_id::text FROM recording_exports e JOIN jobs j ON j.id=e.job_id WHERE j.state='succeeded' AND (e.updated_at<now()-interval '7 days' OR (SELECT coalesce(sum(x.size),0) FROM recording_exports x JOIN jobs y ON y.id=x.job_id WHERE y.state='succeeded')>1073741824) ORDER BY e.updated_at LIMIT 4 FOR UPDATE OF e SKIP LOCKED`)
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if e = os.Remove(filepath.Join(a.config.MP4Directory, id+".mp4")); e != nil && !os.IsNotExist(e) {
				return e
			}
			if _, e = a.db.Exec(c, `UPDATE jobs SET state='failed',reason='cache_expired' WHERE id=$1`, id); e != nil {
				return e
			}
			_, _ = a.db.Exec(c, `UPDATE recording_exports SET size=0 WHERE job_id=$1`, id)
		}
		return nil
	})
	entries, e := os.ReadDir(a.config.MP4Directory)
	if e != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) < 40 || !resource.ValidID(name[:36]) {
			continue
		}
		var keep bool
		e = a.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM recording_exports e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=$1 AND (j.state='running' OR ($2 AND j.state='succeeded')))`, name[:36], name == name[:36]+".mp4").Scan(&keep)
		if e == nil && !keep {
			_ = os.Remove(filepath.Join(a.config.MP4Directory, name))
		}
	}
}
