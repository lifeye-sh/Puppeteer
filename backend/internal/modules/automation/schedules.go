package automation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"next-terminal/backend/internal/platform/postgres"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"
)

type Schedule struct {
	ID             string     `json:"id"`
	OwnerID        string     `json:"ownerId"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	Command        string     `json:"command"`
	AssetIDs       []string   `json:"assetIds"`
	Cron           string     `json:"cron"`
	Timezone       string     `json:"timezone"`
	Enabled        bool       `json:"enabled"`
	Misfire        string     `json:"misfire"`
	TimeoutSeconds int        `json:"timeoutSeconds"`
	SourceIP       string     `json:"sourceIp"`
	NextRun        *time.Time `json:"nextRun"`
	Version        int64      `json:"version"`
	PauseReason    string     `json:"pauseReason"`
}

func ParseSchedule(spec, zone string) (cron.Schedule, error) {
	if len(spec) > 200 || len(strings.Fields(spec)) != 5 || strings.Contains(spec, "=") {
		return nil, fault.Invalid
	}
	if _, e := time.LoadLocation(zone); e != nil || zone == "" || zone == "Local" {
		return nil, fault.Invalid
	}
	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	c, e := p.Parse("CRON_TZ=" + zone + " " + spec)
	if e != nil {
		return nil, fault.Invalid
	}
	return c, nil
}
func NextRuns(spec, zone string, after time.Time) ([]time.Time, error) {
	c, e := ParseSchedule(spec, zone)
	if e != nil {
		return nil, e
	}
	out := []time.Time{}
	for i := 0; i < 10; i++ {
		after = c.Next(after)
		if after.IsZero() {
			return nil, fault.Invalid
		}
		out = append(out, after)
	}
	return out, nil
}
func ValidateSchedule(v *Schedule) error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Kind == "credential.rotate" {
		v.Command = "credential.rotate"
		v.TimeoutSeconds = 60
	}
	if (v.Kind != "ssh.command" && v.Kind != "credential.rotate") || v.Name == "" || utf8.RuneCountInString(v.Name) > 120 || strings.TrimSpace(v.Command) == "" || len(v.Command) > 4096 || strings.ContainsRune(v.Command, 0) || len(v.AssetIDs) < 1 || len(v.AssetIDs) > 20 || v.TimeoutSeconds < 1 || v.TimeoutSeconds > 600 || (v.Misfire != "skip" && v.Misfire != "fire_once") {
		return fault.Invalid
	}
	seen := map[string]bool{}
	for _, id := range v.AssetIDs {
		if !resource.ValidID(id) || seen[id] {
			return fault.Invalid
		}
		seen[id] = true
	}
	if v.Cron != "" {
		_, e := NextRuns(v.Cron, v.Timezone, time.Now())
		if e != nil {
			return e
		}
	} else if v.Enabled {
		return fault.Invalid
	}
	return nil
}

const scheduleColumns = `id,owner_id,name,kind,command,asset_ids,cron,timezone,enabled,misfire,timeout_seconds,source_ip,next_run,version,pause_reason`

func scanSchedule(row pgx.Row) (v Schedule, e error) {
	e = row.Scan(&v.ID, &v.OwnerID, &v.Name, &v.Kind, &v.Command, &v.AssetIDs, &v.Cron, &v.Timezone, &v.Enabled, &v.Misfire, &v.TimeoutSeconds, &v.SourceIP, &v.NextRun, &v.Version, &v.PauseReason)
	return
}
func (s *Service) Schedule(ctx context.Context, id string, lock bool) (Schedule, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return scanSchedule(s.db.Row(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE id=$1 AND deleted_at IS NULL`+suffix, id))
}
func (s *Service) Schedules(ctx context.Context, actor string, admin bool, page int) ([]Schedule, int, error) {
	var total int
	if e := s.db.Row(ctx, `SELECT count(*) FROM schedules WHERE deleted_at IS NULL AND (owner_id=$1 OR $2)`, actor, admin).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE deleted_at IS NULL AND (owner_id=$1 OR $2) ORDER BY created_at DESC,id LIMIT 30 OFFSET $3`, actor, admin, (page-1)*30)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		v, e := scanSchedule(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// Save runs in the application transaction, with owner and source IP set by the server.
func (s *Service) SaveSchedule(ctx context.Context, v Schedule) (string, error) {
	if v.Enabled {
		if e := s.db.RequireFeature(ctx, "automation.execute"); e != nil {
			return "", e
		}
	}
	if e := ValidateSchedule(&v); e != nil {
		return "", e
	}
	var next *time.Time
	if v.Enabled {
		times, _ := NextRuns(v.Cron, v.Timezone, time.Now())
		next = &times[0]
	}
	if v.ID == "" {
		var id string
		e := s.db.Row(ctx, `INSERT INTO schedules(owner_id,name,kind,command,asset_ids,cron,timezone,enabled,misfire,timeout_seconds,source_ip,next_run) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, v.OwnerID, v.Name, v.Kind, v.Command, v.AssetIDs, v.Cron, v.Timezone, v.Enabled, v.Misfire, v.TimeoutSeconds, v.SourceIP, next).Scan(&id)
		return id, e
	}
	previous, e := s.Schedule(ctx, v.ID, true)
	if e != nil {
		return "", e
	}
	if previous.Kind != v.Kind {
		return "", fault.Conflict
	}
	tag, e := s.db.Exec(ctx, `UPDATE schedules SET name=$3,command=$4,asset_ids=$5,cron=$6,timezone=$7,enabled=$8,misfire=$9,timeout_seconds=$10,source_ip=$11,next_run=$12,version=version+1,pause_reason='',updated_at=now() WHERE id=$1 AND owner_id=$2 AND version=$13 AND deleted_at IS NULL`, v.ID, v.OwnerID, v.Name, v.Command, v.AssetIDs, v.Cron, v.Timezone, v.Enabled, v.Misfire, v.TimeoutSeconds, v.SourceIP, next, v.Version)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	return v.ID, nil
}
func (s *Service) DeleteSchedule(ctx context.Context, id, owner string, version int64) error {
	tag, e := s.db.Exec(ctx, `UPDATE schedules SET deleted_at=now(),enabled=false,version=version+1 WHERE id=$1 AND owner_id=$2 AND version=$3 AND NOT EXISTS(SELECT 1 FROM scheduled_runs r JOIN jobs j ON j.id=r.id WHERE r.schedule_id=$1 AND j.state IN ('queued','running'))`, id, owner, version)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

// Caller holds the schedule row lock. The trigger key deduplicates retries and concurrent schedulers.
func (s *Service) Trigger(ctx context.Context, q *jobs.Queue, v Schedule, key string, at *time.Time, skipReason string) (string, error) {
	var old string
	e := s.db.Row(ctx, `SELECT id FROM scheduled_runs WHERE schedule_id=$1 AND trigger_key=$2`, v.ID, key).Scan(&old)
	if e == nil {
		return old, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return "", e
	}
	if e = s.db.RequireFeature(ctx, "automation.execute"); e != nil {
		if at == nil || !errors.Is(e, postgres.ErrAdmission) {
			return "", e
		}
		// Consume this cron occurrence without replaying missed work on renewal.
		skipReason = "entitlement_unavailable"
	}
	var active bool
	if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_runs r JOIN jobs j ON j.id=r.id WHERE r.schedule_id=$1 AND j.state IN ('queued','running'))`, v.ID).Scan(&active); e != nil {
		return "", e
	}
	if active {
		if at == nil {
			return "", fault.Conflict
		}
		skipReason = "overlap_skipped"
	}
	state := "queued"
	if skipReason != "" {
		state = "skipped"
	}
	id, e := q.Enqueue(ctx, v.Kind, state, skipReason)
	if e != nil {
		return "", e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	if _, e = s.db.Exec(ctx, `INSERT INTO scheduled_runs(id,schedule_id,actor_id,trigger_key,snapshot,scheduled_at) VALUES($1,$2,$3,$4,$5,$6)`, id, v.ID, v.OwnerID, key, raw, at); e != nil {
		return "", e
	}
	if state == "queued" {
		for _, asset := range v.AssetIDs {
			if _, e = s.db.Exec(ctx, `INSERT INTO scheduled_run_targets(run_id,asset_id) VALUES($1,$2)`, id, asset); e != nil {
				return "", e
			}
		}
	}
	return id, nil
}

// Each transaction consumes one due schedule. Missed occurrences are coalesced, never flooded.
func (s *Service) Tick(ctx context.Context, q *jobs.Queue) error {
	return s.db.Within(ctx, func(c context.Context) error {
		v, e := scanSchedule(s.db.Row(c, `SELECT `+scheduleColumns+` FROM schedules WHERE enabled AND deleted_at IS NULL AND next_run<=now() ORDER BY next_run FOR UPDATE SKIP LOCKED LIMIT 1`))
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		var now time.Time
		if e = s.db.Row(c, `SELECT now()`).Scan(&now); e != nil {
			return e
		}
		times, e := NextRuns(v.Cron, v.Timezone, now)
		if e != nil {
			return e
		}
		reason := ""
		if now.Sub(*v.NextRun) > time.Minute && v.Misfire == "skip" {
			reason = "misfire_skipped"
		}
		if _, e = s.Trigger(c, q, v, "cron:"+v.NextRun.UTC().Format(time.RFC3339Nano), v.NextRun, reason); e != nil {
			return e
		}
		_, e = s.db.Exec(c, `UPDATE schedules SET next_run=$2 WHERE id=$1`, v.ID, times[0])
		return e
	})
}
