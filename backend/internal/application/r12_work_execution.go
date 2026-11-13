package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/mysqlproxy"
	"strconv"
	"time"
	"unicode/utf8"
)

func (a *App) RunDatabaseWorkOrders(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if a.maintenance.Load() {
				continue
			}
			service := database.New(a.db)
			if e := a.expireDatabaseWorkLeases(ctx); e != nil {
				continue
			}
			work, e := service.ClaimWork(ctx)
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				slog.Error("SQL work order claim failed")
				continue
			}
			a.executeDatabaseWork(ctx, work)
		}
	}
}

func (a *App) expireDatabaseWorkLeases(ctx context.Context) error {
	return a.db.Within(ctx, func(c context.Context) error {
		rows, e := a.db.Rows(c, `SELECT id::text FROM database_work_orders WHERE state='running' AND heartbeat_at<now()-interval '30 seconds' LIMIT 20 FOR UPDATE SKIP LOCKED`)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		service := database.New(a.db)
		for _, id := range ids {
			v, e := service.Work(c, id, false)
			if e != nil {
				return e
			}
			if e = service.FinishWork(c, v, "unknown", "执行进程失去心跳，请人工核对目标库；禁止自动重试"); e != nil {
				return e
			}
			if e = service.FinalizeWorkResults(c, id, "unknown"); e != nil {
				return e
			}
			if _, e = a.db.Exec(c, `UPDATE database_sql_logs SET state='unknown',finished_at=now() WHERE work_order_id=$1 AND (state='running' OR (state='completed' AND $2='transaction'))`, id, v.Mode); e != nil {
				return e
			}
			if e = a.notifications().EnqueueDatabase(c, id+":finished", "database.finished", id, v.RequesterID, nil); e != nil {
				return e
			}
			if e = a.audit.Detail(c, v.RequesterID, "database.work_unknown", v.RunID, "", map[string]string{"id": id, "reason": "worker_lease_expired"}); e != nil {
				return e
			}
		}
		return nil
	})
}
func (a *App) validateDatabaseWork(ctx context.Context, v database.WorkOrder) (database.Asset, error) {
	service := database.New(a.db)
	asset, e := service.Asset(ctx, v.AssetID)
	if e != nil {
		return asset, e
	}
	if asset.Version != v.AssetVersion || database.SQLHash(v.SQL) != v.Hash {
		return asset, fault.Conflict
	}
	if e = a.databaseWorkAccess(ctx, v.RequesterID, v.AssetID); e != nil {
		return asset, e
	}
	reviewer, e := a.identity.Subject(ctx, v.ReviewerID)
	if e != nil {
		return asset, e
	}
	if !reviewer.Enabled || v.ReviewerID == v.RequesterID || a.allowed(ctx, &reviewer.User, "database.approve") != nil {
		return asset, fault.Denied
	}
	if e = a.databaseWorkAccess(ctx, v.ReviewerID, v.AssetID); e != nil {
		return asset, e
	}
	credential, e := a.vault.Get(ctx, asset.WriteCredentialID)
	if e != nil {
		return asset, e
	}
	if credential.ActiveVersion != v.CredentialVersion {
		return asset, fault.Conflict
	}
	return asset, nil
}
func (a *App) finishDatabaseWork(v database.WorkOrder, state, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	service := database.New(a.db)
	e := a.db.Within(ctx, func(c context.Context) error {
		if e := service.FinishWork(c, v, state, reason); e != nil {
			return e
		}
		resultState := "unknown"
		if state == "succeeded" {
			resultState = "completed"
		} else if state == "failed" || state == "cancelled" {
			resultState = "rolled_back"
		}
		if e := service.FinalizeWorkResults(c, v.ID, resultState); e != nil {
			return e
		}
		if v.Mode == "transaction" && (state == "failed" || state == "cancelled") {
			if _, e := a.db.Exec(c, `UPDATE database_sql_logs SET state='rolled_back',finished_at=now() WHERE work_order_id=$1 AND state IN ('running','completed')`, v.ID); e != nil {
				return e
			}
		} else if state == "unknown" {
			if _, e := a.db.Exec(c, `UPDATE database_sql_logs SET state='unknown',finished_at=now() WHERE work_order_id=$1 AND (state='running' OR (state='completed' AND $2='transaction'))`, v.ID, v.Mode); e != nil {
				return e
			}
		}
		if e := a.notifications().EnqueueDatabase(c, v.ID+":finished", "database.finished", v.ID, v.RequesterID, nil); e != nil {
			return e
		}
		return a.audit.Detail(c, v.RequesterID, "database.work_"+state, v.RunID, "", map[string]string{"id": v.ID, "sqlHash": v.Hash, "executionId": v.RunID})
	})
	if e != nil {
		slog.Error("SQL work order finalization failed", "order", v.ID)
	}
}
func (a *App) executeDatabaseWork(parent context.Context, v database.WorkOrder) {
	a.executeDatabaseWorkWithConnector(parent, v, mysqlproxy.ConnectTarget)
}
func (a *App) executeDatabaseWorkWithConnector(parent context.Context, v database.WorkOrder, connect func(context.Context, mysqlproxy.Target, bool) (*client.Conn, error)) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	service := database.New(a.db)
	state, reason := "failed", "执行前验证失败"
	defer func() { a.finishDatabaseWork(v, state, reason) }()
	asset, e := a.validateDatabaseWork(ctx, v)
	if e != nil {
		return
	}
	if e = service.WorkHeartbeat(ctx, v.ID, v.RunID); e != nil {
		state = "cancelled"
		reason = "执行前已取消"
		return
	}
	policy, e := database.NewSQLPolicy()
	if e != nil {
		return
	}
	plan, e := policy.WorkOrder(v.SQL, asset.Database)
	if e != nil || plan.Mode != v.Mode {
		return
	}
	var endpoint mysqlproxy.Target
	e = a.db.Within(ctx, func(c context.Context) error {
		credential, secret, e := a.vault.DatabaseAuth(c, asset.WriteCredentialID, v.RequesterID, v.RunID)
		if e != nil {
			return e
		}
		if credential.ActiveVersion != v.CredentialVersion || credential.Kind != "password" {
			return fault.Conflict
		}
		endpoint = mysqlproxy.Target{Address: net.JoinHostPort(asset.Host, strconv.Itoa(asset.Port)), User: credential.Username, Password: secret.Password, Database: asset.Database, ServerName: asset.TLSServerName, CA: asset.TLSCA, TLSMode: asset.TLSMode, Timeout: time.Duration(asset.ConnectTimeout) * time.Second}
		return nil
	})
	if e != nil {
		return
	}
	target, e := connect(ctx, endpoint, false)
	endpoint.Password = ""
	if e != nil {
		reason = "目标数据库连接或执行账户权限校验失败"
		return
	}
	defer target.Close()
	stop := context.AfterFunc(ctx, func() { target.Close() })
	defer stop()
	heartbeatDone := make(chan struct{})
	defer func() { cancel(); <-heartbeatDone }()
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				check, done := context.WithTimeout(ctx, 3*time.Second)
				e := service.WorkHeartbeat(check, v.ID, v.RunID)
				if e == nil {
					_, e = a.validateDatabaseWork(check, v)
				}
				done()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	// Transactional work must not write through a view or a nontransactional
	// engine. Every referenced write-statement table is checked before BEGIN.
	for _, table := range plan.WriteTables {
		result, e := target.Execute("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND TABLE_TYPE='BASE TABLE' AND ENGINE='InnoDB'", asset.Database, table)
		if e != nil {
			return
		}
		count, e := result.GetInt(0, 0)
		if e != nil || count != 1 {
			reason = "事务工单仅支持当前数据库的 InnoDB 基表"
			return
		}
	}
	transaction := plan.Mode == "transaction"
	if transaction {
		if _, e = target.Execute("START TRANSACTION"); e != nil {
			reason = "无法开启数据库事务"
			return
		}
	}
	completed := 0
	for index, sql := range plan.Statements {
		if e = service.WorkHeartbeat(ctx, v.ID, v.RunID); e == nil {
			_, e = a.validateDatabaseWork(ctx, v)
		}
		if e != nil {
			if transaction {
				if rollbackDatabaseWork(target) {
					state = "cancelled"
					reason = "执行已取消，事务已回滚"
					return
				}
			}
			state = "unknown"
			reason = "执行中取消或授权失效，请核对目标库"
			return
		}
		start := time.Now()
		queryID, auditErr := service.BeginWorkQuery(ctx, v.ID, sql)
		if auditErr != nil {
			reason = "SQL 审计写入失败，已停止后续执行"
			if transaction {
				if rollbackDatabaseWork(target) {
					return
				}
				state = "unknown"
			} else if completed > 0 {
				state = "partial"
			}
			return
		}
		var affected uint64
		var data []byte
		if plan.ReadStatements[index] {
			data, e = collectDatabaseRows(target, sql)
		} else {
			var result *mysql.Result
			result, e = target.Execute(sql)
			if e == nil {
				affected = result.AffectedRows
			}
		}
		if e != nil {
			if ctx.Err() == nil {
				_ = service.FinishQuery(ctx, queryID, true, 0, time.Since(start))
			}
			var mysqlError *mysql.MyError
			if !errors.As(e, &mysqlError) {
				state = "unknown"
				reason = "数据库连接中断，执行结果不确定；禁止自动重试"
				return
			}
			reason = fmt.Sprintf("第 %d 条语句失败（MySQL %d）", index+1, mysqlError.Code)
			_ = service.SaveWorkResult(ctx, v.ID, index, "failed", 0, nil, time.Since(start))
			if transaction {
				if !rollbackDatabaseWork(target) {
					state = "unknown"
					reason = "回滚确认失败，请人工核对目标库"
					return
				}
			} else if completed > 0 {
				state = "partial"
			}
			return
		}
		resultState := "completed"
		loggedRows := affected
		if len(data) > 0 {
			var summary struct {
				Total uint64 `json:"total"`
			}
			if json.Unmarshal(data, &summary) == nil {
				loggedRows = summary.Total
			}
		}
		if auditErr = service.FinishQuery(ctx, queryID, false, loggedRows, time.Since(start)); auditErr != nil {
			if transaction {
				if rollbackDatabaseWork(target) {
					reason = "审计结果写入失败，事务已回滚"
					return
				}
			}
			state = "unknown"
			reason = "审计结果写入失败，请人工核对目标库"
			return
		}
		if transaction {
			resultState = "pending_commit"
		}
		if e = service.SaveWorkResult(ctx, v.ID, index, resultState, affected, data, time.Since(start)); e != nil {
			if transaction {
				if rollbackDatabaseWork(target) {
					reason = "结果持久化失败，事务已回滚"
					return
				}
			}
			state = "unknown"
			reason = "结果持久化失败，请人工核对目标库"
			return
		}
		completed++
	}
	if transaction {
		e = service.WorkHeartbeat(ctx, v.ID, v.RunID)
		if e == nil {
			_, e = a.validateDatabaseWork(ctx, v)
		}
		if e != nil {
			if rollbackDatabaseWork(target) {
				state = "cancelled"
				reason = "提交前取消或授权失效，事务已回滚"
				return
			}
			state = "unknown"
			reason = "取消时未能确认事务状态"
			return
		}
		if _, e = target.Execute("COMMIT"); e != nil {
			state = "unknown"
			reason = "未收到事务提交确认，请核对目标库；禁止自动重试"
			return
		}
	}
	state = "succeeded"
	reason = "执行完成"
}
func rollbackDatabaseWork(target *client.Conn) bool {
	result, e := target.Execute("ROLLBACK")
	// MySQL warning 1196 signals nontransactional side effects, including
	// tables touched by a trigger. Never label such a rollback as confirmed.
	return e == nil && result != nil && result.Warnings == 0
}
func collectDatabaseRows(target *client.Conn, sql string) ([]byte, error) {
	columns := []string{}
	rows := [][]any{}
	count, bytesUsed := 0, 0
	var result mysql.Result
	e := target.ExecuteSelectStreaming(sql, &result, func(values []mysql.FieldValue) error {
		count++
		if len(rows) >= 1000 {
			return nil
		}
		row := make([]any, len(values))
		size := 0
		for i, value := range values {
			row[i] = value.Value()
			switch v := row[i].(type) {
			case []byte:
				size += len(v) * 2
				if utf8.Valid(v) {
					row[i] = string(v)
				} else {
					row[i] = map[string]any{"base64": base64.StdEncoding.EncodeToString(v), "bytes": len(v)}
				}
			case string:
				size += len(v) * 2
			default:
				size += 32
			}
		}
		if bytesUsed+size > 2<<20 {
			return nil
		}
		bytesUsed += size
		rows = append(rows, row)
		return nil
	}, func(result *mysql.Result) error {
		for _, field := range result.Fields {
			name := string(field.Name)
			if len(name) > 256 {
				name = name[:256]
			}
			columns = append(columns, name)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"columns": columns, "rows": rows, "total": count, "truncated": count > len(rows)})
}
