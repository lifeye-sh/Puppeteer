package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/packet"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/platform/mysqlproxy"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type dropCommitAcknowledgement struct {
	net.Conn
	commit bool
}

type afterStatementWrite struct {
	net.Conn
	match string
	once  sync.Once
	after func()
}

func (c *afterStatementWrite) Write(data []byte) (int, error) {
	n, e := c.Conn.Write(data)
	if e == nil && len(data) > 5 && data[4] == mysql.COM_QUERY && strings.Contains(string(data[5:]), c.match) {
		c.once.Do(c.after)
	}
	return n, e
}

func (c *dropCommitAcknowledgement) Write(data []byte) (int, error) {
	if len(data) >= 11 && data[4] == mysql.COM_QUERY && strings.EqualFold(string(data[5:]), "COMMIT") {
		c.commit = true
	}
	return c.Conn.Write(data)
}
func (c *dropCommitAcknowledgement) Read(data []byte) (int, error) {
	n, e := c.Conn.Read(data)
	if c.commit && n > 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return n, e
}
func TestR12WorkOrdersCommitRollbackAndUncertainty(t *testing.T) {
	directory := os.Getenv("NT_R12_MYSQL_FIXTURE")
	if directory == "" {
		t.Skip("isolated MySQL fixture not configured")
	}
	raw, e := os.ReadFile(filepath.Join(directory, "credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	var secrets map[string]string
	if e = json.Unmarshal(raw, &secrets); e != nil {
		t.Fatal(e)
	}
	clear(raw)
	ca, e := os.ReadFile(filepath.Join(directory, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "SQL approver", "permissions": []string{"database.approve"}}, admin)
	user := postID(t, srv, "/api/admin/users", userInput("sql-requester", []string{role}, nil), admin)
	requester := loginAs(t, srv, "sql-requester")
	credential := func(name string) string {
		return postID(t, srv, "/api/admin/credentials", map[string]any{"name": "R12 " + name, "kind": "password", "username": name, "secret": map[string]string{"password": secrets[name]}}, admin)
	}
	asset := postID(t, srv, "/api/admin/database-assets", map[string]any{"name": "Work target", "alias": "work_target", "host": "nt-r12-mysql-fixture", "port": 3306, "database": "inventory", "readonlyCredentialId": credential("reader"), "writeCredentialId": credential("writer"), "tlsServerName": "nt-r12-mysql-fixture", "tlsCa": string(ca), "enabled": true, "connectTimeout": 10}, admin)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	diagnostics := object(t, call(t, srv.URL, "POST", "/api/admin/database-assets/"+asset+"/test", map[string]any{}, admin, origin))["items"].([]any)
	if len(diagnostics) != 2 || diagnostics[0].(map[string]any)["ok"] != true || diagnostics[1].(map[string]any)["ok"] != true {
		t.Fatal("target connection diagnostics failed")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/database-assets/"+asset+"/test", map[string]any{}, requester, origin), 403)
	control, e := mysqlproxy.ConnectTarget(ctx, mysqlproxy.Target{Address: "nt-r12-mysql-fixture:3306", User: "writer", Password: secrets["writer"], Database: "inventory", ServerName: "nt-r12-mysql-fixture", CA: string(ca), Timeout: 10 * time.Second}, false)
	if e != nil {
		t.Fatal(e)
	}
	defer control.Close()
	table := fmt.Sprintf("work_%x", time.Now().UnixNano())
	defer func() { _, _ = control.Execute("DROP TABLE IF EXISTS " + table) }()
	service := database.New(a.db)
	submit := func(sql string, ddl bool) string {
		return postID(t, srv, "/api/database-work-orders", map[string]any{"assetId": asset, "title": "R12 fixture work", "sql": sql, "acknowledgeDdl": ddl}, requester)
	}
	approve := func(id string) {
		expect(t, call(t, srv.URL, "POST", "/api/database-work-orders/"+id+"/review", map[string]any{"version": 1, "approve": true}, admin, origin), 204)
	}
	run := func(id, want string, dropCommit bool) {
		work, e := service.ClaimWork(ctx)
		if e != nil || work.ID != id {
			t.Fatal("claim", e, work.ID)
		}
		if dropCommit {
			a.executeDatabaseWorkWithConnector(ctx, work, func(c context.Context, target mysqlproxy.Target, readonly bool) (*client.Conn, error) {
				connection, e := mysqlproxy.ConnectTarget(c, target, readonly)
				if e == nil {
					connection.Conn = packet.NewConn(&dropCommitAcknowledgement{Conn: connection.Conn.Conn})
				}
				return connection, e
			})
		} else {
			a.executeDatabaseWork(ctx, work)
		}
		result, e := service.Work(ctx, id, false)
		if e != nil || result.State != want {
			t.Fatalf("work state %s want %s, reason=%s error=%v", result.State, want, result.Reason, e)
		}
	}
	create := submit("CREATE TABLE "+table+"(id INT PRIMARY KEY,label VARCHAR(100)) ENGINE=InnoDB", true)
	expect(t, call(t, srv.URL, "POST", "/api/database-work-orders/"+create+"/review", map[string]any{"version": 1, "approve": true}, requester, origin), 403)
	approve(create)
	run(create, "succeeded", false)
	rollback := submit("INSERT INTO "+table+" VALUES(1,'first'); INSERT INTO "+table+" VALUES(1,'duplicate')", false)
	approve(rollback)
	run(rollback, "failed", false)
	count := func(want int64) {
		result, e := control.Execute("SELECT COUNT(*) FROM " + table)
		if e != nil {
			t.Fatal(e)
		}
		n, e := result.GetInt(0, 0)
		if e != nil || n != want {
			t.Fatal("target row count", n, want, e)
		}
	}
	count(0)
	success := submit("INSERT INTO "+table+" VALUES(1,'committed'); /* comment */ SELECT label FROM "+table, false)
	approve(success)
	run(success, "succeeded", false)
	count(1)
	results, e := service.WorkResults(ctx, success, 0, 10)
	if e != nil || len(results) != 2 || !strings.Contains(string(results[1].Data), "committed") {
		t.Fatal("result capture", e)
	}
	partial := submit("ALTER TABLE "+table+" ADD extra INT; ALTER TABLE "+table+" ADD extra INT", true)
	approve(partial)
	run(partial, "partial", false)
	uncertain := submit("INSERT INTO "+table+"(id,label) VALUES(2,'ack lost')", false)
	approve(uncertain)
	run(uncertain, "unknown", true)
	count(2)
	if _, e = service.ClaimWork(ctx); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("uncertain work was automatically retried", e)
	}
	cancelled := submit("DELETE FROM "+table, false)
	expect(t, call(t, srv.URL, "POST", "/api/database-work-orders/"+cancelled+"/cancel", map[string]any{"version": 1}, requester, origin), 204)
	expect(t, call(t, srv.URL, "POST", "/api/database-work-orders/"+cancelled+"/review", map[string]any{"version": 2, "approve": true}, admin, origin), 403)
	// Cancel an actual target statement waiting on a row lock. A dropped target
	// connection is conservatively unknown, and never rescheduled.
	if _, e = control.Execute("START TRANSACTION"); e != nil {
		t.Fatal(e)
	}
	if _, e = control.Execute("SELECT id FROM " + table + " WHERE id=1 FOR UPDATE"); e != nil {
		t.Fatal(e)
	}
	blocked := submit("UPDATE "+table+" SET label='must not commit' WHERE id=1", false)
	approve(blocked)
	claimed, e := service.ClaimWork(ctx)
	if e != nil || claimed.ID != blocked {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); a.executeDatabaseWork(ctx, claimed) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var started int
		if e = pool.QueryRow(ctx, `SELECT count(*) FROM database_sql_logs WHERE work_order_id=$1 AND state='running'`, blocked).Scan(&started); e != nil {
			t.Fatal(e)
		}
		if started > 0 {
			break
		}
		if time.Now().After(deadline) {
			outcome, _ := service.Work(ctx, blocked, false)
			t.Fatalf("blocked statement did not start: state=%s reason=%s", outcome.State, outcome.Reason)
		}
		time.Sleep(10 * time.Millisecond)
	}
	expect(t, call(t, srv.URL, "POST", "/api/database-work-orders/"+blocked+"/cancel", map[string]any{"version": claimed.Version}, requester, origin), 204)
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("running cancellation did not close target")
	}
	if _, e = control.Execute("ROLLBACK"); e != nil {
		t.Fatal(e)
	}
	interrupted, e := service.Work(ctx, blocked, false)
	if e != nil || interrupted.State != "unknown" || !interrupted.CancelRequested {
		t.Fatal("cancelled statement incorrectly confirmed", interrupted.State, e)
	}
	if _, e = service.ClaimWork(ctx); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("interrupted work retried", e)
	}
	var events int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE database_order_id=$1 AND event_type='database.finished'`, uncertain).Scan(&events); e != nil || events != 1 {
		t.Fatal("missing durable notification", events, e)
	}
	revoked := submit("INSERT INTO "+table+"(id,label) VALUES(3,'revoke-before-commit')", false)
	approve(revoked)
	toRevoke, e := service.ClaimWork(ctx)
	if e != nil || toRevoke.ID != revoked {
		t.Fatal(e)
	}
	var revokeError error
	a.executeDatabaseWorkWithConnector(ctx, toRevoke, func(c context.Context, target mysqlproxy.Target, readonly bool) (*client.Conn, error) {
		connection, e := mysqlproxy.ConnectTarget(c, target, readonly)
		if e != nil {
			return nil, e
		}
		wrapped := &afterStatementWrite{Conn: connection.Conn.Conn, match: "revoke-before-commit", after: func() {
			_, revokeError = pool.Exec(ctx, `UPDATE grants SET revoked_at=now(),version=version+1 WHERE user_id=$1 AND resource_id=$2`, user, asset)
		}}
		connection.Conn = packet.NewConnWithTimeout(wrapped, connection.ReadTimeout, connection.WriteTimeout, connection.BufferSize)
		return connection, nil
	})
	if revokeError != nil {
		t.Fatal(revokeError)
	}
	afterRevoke, e := service.Work(ctx, revoked, false)
	if e != nil || afterRevoke.State != "cancelled" {
		t.Fatal("revocation before COMMIT was ignored", afterRevoke.State, e)
	}
	count(2)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	stale := submit("DELETE FROM "+table, false)
	approve(stale)
	if _, e = pool.Exec(ctx, `UPDATE resource_registry SET version=version+1 WHERE id=$1`, asset); e != nil {
		t.Fatal(e)
	}
	run(stale, "failed", false)
	count(2)
	// MySQL explicitly warns when a rollback leaves nontransactional effects.
	nontransactional := table + "_myisam"
	if _, e = control.Execute("CREATE TABLE " + nontransactional + "(id INT) ENGINE=MyISAM"); e != nil {
		t.Fatal(e)
	}
	defer func() { _, _ = control.Execute("DROP TABLE IF EXISTS " + nontransactional) }()
	if _, e = control.Execute("START TRANSACTION"); e != nil {
		t.Fatal(e)
	}
	if _, e = control.Execute("INSERT INTO " + nontransactional + " VALUES(1)"); e != nil {
		t.Fatal(e)
	}
	if rollbackDatabaseWork(control) {
		t.Fatal("rollback with nontransactional warning was marked confirmed")
	}
}
