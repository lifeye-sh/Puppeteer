package system

import (
	"context"
	"errors"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrRetentionPreviewRequired = errors.New("retention preview required")

// SQL fragments below are a closed internal registry, never request-provided identifiers.
type RetentionCategory struct {
	ID                         string `json:"id"`
	Key                        string `json:"key"`
	Label                      string `json:"label"`
	table, timestamp, eligible string
}

func RetentionCategories() []RetentionCategory {
	return []RetentionCategory{
		{"session", "session-saved-limit-days", "会话内容（录屏与命令）", "sessions", "t.closed_at", "audit_purged_at IS NULL AND recording_purged_at IS NULL AND closed_at IS NOT NULL"},
		{"access", "access-log-saved-limit-days", "访问记录与录屏", "sessions", "t.closed_at", "audit_purged_at IS NULL AND closed_at IS NOT NULL"},
		{"login", "login-log-saved-limit-days", "登录日志", "login_logs", "t.created_at", "true"},
		{"cron", "cron-log-saved-limit-days", "定时任务日志", "scheduled_runs", "(SELECT ended_at FROM jobs WHERE id=t.id)", "audit_purged_at IS NULL AND EXISTS(SELECT 1 FROM jobs WHERE id=t.id AND state NOT IN ('queued','running') AND ended_at IS NOT NULL)"},
		{"sql", "db-sql-log-saved-limit-days", "数据库 SQL 日志", "database_sql_logs", "t.finished_at", "finished_at IS NOT NULL AND state<>'running'"},
		{"operation", "operation-log-saved-limit-days", "管理操作日志", "operation_logs", "t.created_at", "true"},
		{"filesystem", "filesystem-log-saved-limit-days", "文件操作日志", "filesystem_logs", "t.ended_at", "ended_at IS NOT NULL AND state<>'running'"},
		{"event", "system-event-saved-limit-days", "系统事件", "system_events", "t.created_at", "true"},
	}
}
func retentionHoldCategory(id string) string {
	if id == "session" {
		return "access"
	}
	return id
}
func category(id string) (RetentionCategory, error) {
	for _, v := range RetentionCategories() {
		if v.ID == id {
			return v, nil
		}
	}
	return RetentionCategory{}, fault.Invalid
}
func validDays(n int) bool { return n == -1 || n >= 1 && n <= 3650 }
func (s *Service) LockAuditRetention(ctx context.Context) error {
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	_, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(680069)`)
	return e
}
func (s *Service) LockedRetentionConfiguration(ctx context.Context) (Revision, error) {
	if !s.db.InTransaction(ctx) {
		return Revision{}, fault.Invalid
	}
	var id int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&id); e != nil {
		return Revision{}, e
	}
	v, e := s.Configuration(ctx, id)
	if e == nil {
		e = ValidateConfiguration(v.Values)
	}
	return v, e
}

type RetentionPreview struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	Days     int    `json:"days"`
	Eligible int64  `json:"eligible"`
	Held     int64  `json:"held"`
}

// A preview does not delete data. It records the exact actor, version and proposed days for ten minutes.
func (s *Service) PreviewRetention(ctx context.Context, actor string, revision int64, days map[string]int) (out []RetentionPreview, err error) {
	if !s.db.InTransaction(ctx) || len(days) == 0 || len(days) > len(RetentionCategories()) {
		return nil, fault.Invalid
	}
	v, e := s.LockedRetentionConfiguration(ctx)
	if e != nil {
		return nil, e
	}
	if v.ID != revision {
		return nil, fault.Conflict
	}
	known := map[string]bool{}
	for _, c := range RetentionCategories() {
		known[c.Key] = true
	}
	for k, n := range days {
		if !known[k] || !validDays(n) {
			return nil, fault.Invalid
		}
	}
	out = []RetentionPreview{}
	for _, c := range RetentionCategories() {
		n, ok := days[c.Key]
		if !ok {
			continue
		}
		p := RetentionPreview{Category: c.ID, Key: c.Key, Label: c.Label, Days: n}
		if n > 0 {
			e = s.db.Row(ctx, `SELECT count(*) FILTER(WHERE h.record_id IS NULL),count(*) FILTER(WHERE h.record_id IS NOT NULL) FROM `+c.table+` t LEFT JOIN audit_holds h ON h.category=$1 AND h.record_id=t.id WHERE `+c.eligible+` AND `+c.timestamp+`<now()-($2*interval '1 day')`, retentionHoldCategory(c.ID), n).Scan(&p.Eligible, &p.Held)
			if e != nil {
				return nil, e
			}
		}
		_, e = s.db.Exec(ctx, `INSERT INTO audit_retention_previews(actor_id,revision,key,days,expires_at) VALUES($1,$2,$3,$4,now()+interval '10 minutes') ON CONFLICT(actor_id,revision,key) DO UPDATE SET days=excluded.days,expires_at=excluded.expires_at`, actor, revision, c.Key, n)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	_, e = s.db.Exec(ctx, `DELETE FROM audit_retention_previews WHERE expires_at<now()`)
	return out, e
}

type AuditHold struct {
	Category  string    `json:"category"`
	RecordID  string    `json:"recordId"`
	Reason    string    `json:"reason"`
	ActorID   string    `json:"actorId"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Service) AuditHolds(ctx context.Context, offset int) ([]AuditHold, int, error) {
	var total int
	if e := s.db.Row(ctx, `SELECT count(*) FROM audit_holds`).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT category,record_id,reason,actor_id,created_at FROM audit_holds ORDER BY created_at DESC,category,record_id LIMIT 50 OFFSET $1`, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []AuditHold{}
	for rows.Next() {
		var v AuditHold
		if e = rows.Scan(&v.Category, &v.RecordID, &v.Reason, &v.ActorID, &v.CreatedAt); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (s *Service) SetAuditHold(ctx context.Context, cid, id, reason, actor string, held bool) error {
	cid = retentionHoldCategory(cid)
	c, e := category(cid)
	if e != nil {
		return e
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 500 {
		return fault.Invalid
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return fault.Invalid
		}
	}
	if e = s.LockAuditRetention(ctx); e != nil {
		return e
	}
	var found string
	extra := ""
	if cid == "access" || cid == "cron" {
		extra = " AND audit_purged_at IS NULL"
	}
	if e = s.db.Row(ctx, `SELECT id FROM `+c.table+` WHERE id=$1`+extra+` FOR UPDATE`, id).Scan(&found); e != nil {
		return e
	}
	if held {
		_, e = s.db.Exec(ctx, `INSERT INTO audit_holds(category,record_id,reason,actor_id) VALUES($1,$2,$3,$4) ON CONFLICT(category,record_id) DO UPDATE SET reason=excluded.reason,actor_id=excluded.actor_id,created_at=now()`, cid, id, reason, actor)
	} else {
		_, e = s.db.Exec(ctx, `DELETE FROM audit_holds WHERE category=$1 AND record_id=$2`, cid, id)
	}
	return e
}

// Caller holds the global retention lock and configuration head share lock for the entire batch.
func (s *Service) RetentionIDs(ctx context.Context, cid string, days int) ([]string, error) {
	c, e := category(cid)
	if e != nil || !validDays(days) || !s.db.InTransaction(ctx) {
		return nil, fault.Invalid
	}
	out := []string{}
	if days == -1 {
		return out, nil
	}
	rows, e := s.db.Rows(ctx, `SELECT t.id FROM `+c.table+` t WHERE `+c.eligible+` AND `+c.timestamp+`<now()-($1*interval '1 day') AND NOT EXISTS(SELECT 1 FROM audit_holds h WHERE h.category=$2 AND h.record_id=t.id) ORDER BY `+c.timestamp+`,t.id LIMIT 25 FOR UPDATE OF t SKIP LOCKED`, days, retentionHoldCategory(cid))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Service) PurgeCategoryRecord(ctx context.Context, cid, id string) error {
	c, e := category(cid)
	if e != nil || cid == "access" || cid == "session" || !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	if e = s.LockAuditRetention(ctx); e != nil {
		return e
	}
	var held bool
	if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM audit_holds WHERE category=$1 AND record_id=$2)`, cid, id).Scan(&held); e != nil {
		return e
	}
	if held {
		return fault.Conflict
	}
	if cid == "cron" {
		// Keep schedule/trigger identity to prevent a scheduler restart from replaying deleted history.
		for _, q := range []string{`DELETE FROM scheduled_run_targets WHERE run_id=$1`, `UPDATE scheduled_runs SET snapshot='{}',audit_purged_at=now() WHERE id=$1`, `UPDATE jobs SET reason='' WHERE id=$1`} {
			if _, e = s.db.Exec(ctx, q, id); e != nil {
				return e
			}
		}
		return nil
	}
	_, e = s.db.Exec(ctx, `DELETE FROM `+c.table+` WHERE id=$1`, id)
	return e
}
