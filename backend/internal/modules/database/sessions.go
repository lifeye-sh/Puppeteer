package database

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) AssetByAlias(ctx context.Context, alias string) (v Asset, e error) {
	e = s.db.Row(ctx, `SELECT `+assetFields+` FROM database_assets d JOIN resource_registry r ON r.id=d.id WHERE d.alias=$1 AND r.deleted_at IS NULL AND r.enabled`, alias).Scan(&v.ID, &v.Name, &v.Alias, &v.Host, &v.Port, &v.Database, &v.ReadonlyCredentialID, &v.WriteCredentialID, &v.TLSServerName, &v.TLSCA, &v.Description, &v.ConnectTimeout, &v.Enabled, &v.Version, &v.Engine, &v.NativeWriteCredentialID, &v.TLSMode, &v.PermissionMode)
	return
}
func (s *Service) BeginSession(ctx context.Context, user string, asset Asset, passwordVersion int64, address string) (id string, e error) {
	if !s.db.InTransaction(ctx) {
		e = s.db.Within(ctx, func(c context.Context) error {
			var err error
			id, err = s.BeginSession(c, user, asset, passwordVersion, address)
			return err
		})
		return
	}
	if e = s.db.Admit(ctx, "sessions", databaseFeature(asset.Engine)); e != nil {
		return
	}
	e = s.db.Row(ctx, `INSERT INTO database_sessions(user_id,asset_id,password_version,asset_version,remote_address,quota_epoch,quota_lease_until) VALUES($1,$2,$3,$4,$5,(SELECT auth_epoch FROM instance_state WHERE singleton=1),clock_timestamp()+interval '75 seconds') RETURNING id`, user, asset.ID, passwordVersion, asset.Version, address).Scan(&id)
	return
}
func databaseFeature(engine string) string {
	if engine == "postgresql" || engine == "postgres" {
		return "database.postgres"
	}
	return "database.mysql"
}
func (s *Service) RenewSession(ctx context.Context, id, engine string) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(c context.Context) error { return s.RenewSession(c, id, engine) })
	}
	if e := s.db.Admit(ctx, "sessions.renew", databaseFeature(engine)); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE database_sessions SET quota_lease_until=clock_timestamp()+interval '15 seconds' WHERE id=$1 AND ended_at IS NULL AND quota_lease_until>clock_timestamp() AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1)`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Denied
	}
	return e
}
func (s *Service) EndSession(ctx context.Context, id, reason string) error {
	return s.db.Within(ctx, func(c context.Context) error {
		if _, e := s.db.Exec(c, `UPDATE database_sessions SET ended_at=now(),end_reason=$2 WHERE id=$1 AND ended_at IS NULL`, id, reason); e != nil {
			return e
		}
		_, e := s.db.Exec(c, `UPDATE database_sql_logs SET state='unknown',finished_at=now() WHERE session_id=$1 AND state='running'`, id)
		return e
	})
}

// Expired processes cannot leave permanently active audit records. One bounded
// statement also finalizes in-flight SQL as unknown; completed SQL is preserved.
func (s *Service) ReapSessions(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `WITH expired AS (
 SELECT id FROM database_sessions WHERE ended_at IS NULL AND (quota_lease_until<=clock_timestamp() OR quota_epoch<>(SELECT auth_epoch FROM instance_state WHERE singleton=1)) ORDER BY quota_lease_until LIMIT 100 FOR UPDATE SKIP LOCKED
 ), closed AS (
 UPDATE database_sessions s SET ended_at=clock_timestamp(),end_reason='lease_expired' FROM expired x WHERE s.id=x.id RETURNING s.id
 ) UPDATE database_sql_logs SET state='unknown',finished_at=clock_timestamp() WHERE state='running' AND session_id IN (SELECT id FROM closed)`)
	return e
}
func (s *Service) BeginQuery(ctx context.Context, session, sql string, command byte, parameters any) (id string, e error) {
	return s.beginQuery(ctx, session, "", sql, command, parameters)
}
func (s *Service) BeginWorkQuery(ctx context.Context, order, sql string) (string, error) {
	return s.beginQuery(ctx, "", order, sql, 3, []any{})
}
func (s *Service) beginQuery(ctx context.Context, session, order, sql string, command byte, parameters any) (id string, e error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(sql)))
	metadata, e := json.Marshal(parameters)
	if e != nil {
		return "", e
	}
	original := sql
	sql = strings.ToValidUTF8(strings.ReplaceAll(sql, "\x00", "\\0"), "�")
	if len(sql) > MaxSQLBytes {
		sql = sql[:MaxSQLBytes]
		for !utf8.ValidString(sql) {
			sql = sql[:len(sql)-1]
		}
	}
	e = s.db.Row(ctx, `INSERT INTO database_sql_logs(session_id,sql_text,sql_sha256,command,parameter_meta,sql_sanitized,work_order_id) VALUES(nullif($1,'')::uuid,$2,$3,$4,$5,$6,nullif($7,'')::uuid) RETURNING id`, session, sql, hash, int(command), metadata, original != sql, order).Scan(&id)
	return
}
func (s *Service) FinishQuery(ctx context.Context, id string, failed bool, rows uint64, duration time.Duration) error {
	state := "completed"
	if failed {
		state = "failed"
	}
	_, e := s.db.Exec(ctx, `UPDATE database_sql_logs SET state=$2,row_count=$3,duration_ms=$4,finished_at=now() WHERE id=$1 AND state='running'`, id, state, rows, duration.Milliseconds())
	return e
}

func (s *Service) UncertainQuery(ctx context.Context, id string, duration time.Duration) error {
	_, e := s.db.Exec(ctx, `UPDATE database_sql_logs SET state='unknown',duration_ms=$2,finished_at=now() WHERE id=$1 AND state='running'`, id, duration.Milliseconds())
	return e
}
