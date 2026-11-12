package application

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

func TestR16PostgresNativeProtocol(t *testing.T) {
	dir := os.Getenv("NT_R16_PG_FIXTURE")
	if dir == "" {
		t.Skip("isolated PostgreSQL18 fixture required")
	}
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("pg-reader", nil, nil), admin)
	reader := loginAs(t, srv, "pg-reader")
	cert, e := os.ReadFile(filepath.Join(dir, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	cred := postID(t, srv, "/api/admin/credentials", map[string]any{"name": "PG native reader", "kind": "password", "username": "reader", "secret": map[string]string{"password": "r16-fixture-reader"}}, admin)
	asset := postID(t, srv, "/api/admin/database-assets", map[string]any{"name": "PG native fixture", "alias": "pgnative", "engine": "postgresql", "host": "r16-postgres-fixture", "port": 5432, "database": "inventory", "readonlyCredentialId": cred, "tlsServerName": "r16-postgres-fixture", "tlsCa": string(cert), "enabled": true, "connectTimeout": 5}, admin)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	password := object(t, call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, reader, origin))["password"].(string)
	a.config.DatabaseProxyName = "localhost"
	tlsConfig, e := a.databaseProxyTLS(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(tlsConfig.Certificates[0].Leaf)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	protocol := a.postgresProtocol(tlsConfig)
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			workers.Add(1)
			go func() { defer workers.Done(); protocol.ServeConn(ctx, c) }()
		}
	}()
	defer func() { listener.Close(); <-acceptDone; cancel(); workers.Wait() }()
	config, e := pgx.ParseConfig("postgresql://pg-reader%40pgnative@" + listener.Addr().String() + "/inventory?sslmode=verify-full")
	if e != nil {
		t.Fatal(e)
	}
	config.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "localhost"}
	config.Fallbacks = nil
	config.Password = password
	config.ConnectTimeout = 5 * time.Second
	config.RuntimeParams["extra_float_digits"] = "3"
	connect := func() (*pgx.Conn, error) { return pgx.ConnectConfig(ctx, config.Copy()) }
	c, e := connect()
	if e != nil {
		t.Fatal("SCRAM connect", e)
	}
	defer c.Close(ctx)
	if jar := os.Getenv("NT_R16_JDBC"); jar != "" {
		caPath := filepath.Join(t.TempDir(), "proxy.pem")
		if e = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsConfig.Certificates[0].Certificate[0]}), 0600); e != nil {
			t.Fatal(e)
		}
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		cmd := exec.CommandContext(ctx, "java", "-Duser.timezone=Asia/Shanghai", "--class-path", jar, "../../test/postgres/ClientMatrix.java")
		cmd.Env = append(os.Environ(), "NT_JDBC_PORT="+port, "NT_JDBC_CA="+caPath, "NT_JDBC_PASSWORD="+password)
		output, err := cmd.CombinedOutput()
		if err != nil {
			var last string
			_ = pool.QueryRow(ctx, `SELECT sql_text FROM database_sql_logs WHERE state='failed' ORDER BY created_at DESC LIMIT 1`).Scan(&last)
			t.Fatalf("JDBC: %v\n%s\nRejected fixture SQL: %s", err, output, last)
		}
		t.Log(string(output))
	}
	if os.Getenv("NT_R16_PSQL") != "" {
		caPath := filepath.Join(t.TempDir(), "proxy.pem")
		if e = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsConfig.Certificates[0].Certificate[0]}), 0600); e != nil {
			t.Fatal(e)
		}
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		cmd := exec.CommandContext(ctx, "psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", "SELECT id,label FROM items ORDER BY id")
		cmd.Env = append(os.Environ(), "PGHOST=localhost", "PGHOSTADDR=127.0.0.1", "PGPORT="+port, "PGDATABASE=inventory", "PGUSER=pg-reader@pgnative", "PGPASSWORD="+password, "PGSSLMODE=verify-full", "PGSSLROOTCERT="+caPath)
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "1|R16 PostgreSQL fixture\n2|\n") {
			t.Fatalf("psql: %v %s", err, output)
		}
	}
	var label string
	var n int32
	if e = c.QueryRow(ctx, "SELECT id,label FROM items WHERE id=$1", int32(1)).Scan(&n, &label); e != nil || n != 1 || label != "R16 PostgreSQL fixture" {
		t.Fatal("extended bind", n, label, e)
	}
	var nullable *string
	if e = c.QueryRow(ctx, "SELECT label FROM items WHERE id=$1", int32(2)).Scan(&nullable); e != nil || nullable != nil {
		t.Fatal("NULL parameter result", e)
	}
	if e = c.QueryRow(ctx, "SELECT $1::text", "binding-value-not-for-audit").Scan(&label); e != nil {
		t.Fatal(e)
	}
	// Simple query and COPY output preserve target framing and data.
	simple := config.Copy()
	simple.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sc, e := pgx.ConnectConfig(ctx, simple)
	if e != nil {
		t.Fatal(e)
	}
	defer sc.Close(ctx)
	if e = sc.QueryRow(ctx, "SELECT count(*) FROM items").Scan(&n); e != nil || n != 2 {
		t.Fatal("simple query", n, e)
	}
	var data bytes.Buffer
	if _, e = sc.PgConn().CopyTo(ctx, &data, "COPY (SELECT id,label FROM items ORDER BY id) TO STDOUT WITH (FORMAT csv)"); e != nil || !strings.Contains(data.String(), "R16 PostgreSQL fixture") {
		t.Fatal("COPY TO", e)
	}
	for _, sql := range []string{"DELETE FROM items", "SELECT * INTO stolen FROM items", "SELECT set_config('default_transaction_read_only','off',false)", "COPY items FROM STDIN"} {
		if _, e = sc.Exec(ctx, sql); e == nil {
			t.Fatal("policy bypass", sql)
		}
		if e = sc.QueryRow(ctx, "SELECT 1").Scan(&n); e != nil {
			t.Fatal("simple recovery", e)
		}
	}
	// A denied extended Parse must recover only after Sync, including in a transaction.
	if _, e = c.Exec(ctx, "BEGIN"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Prepare(ctx, "bad", "DELETE FROM items"); e == nil {
		t.Fatal("unsafe prepare")
	}
	if _, e = c.Exec(ctx, "SELECT 1"); e == nil {
		t.Fatal("policy error did not abort transaction")
	}
	if _, e = c.Exec(ctx, "ROLLBACK"); e != nil {
		t.Fatal("transaction recovery", e)
	}
	if e = c.QueryRow(ctx, "SELECT 1").Scan(&n); e != nil {
		t.Fatal("Sync recovery", e)
	}
	// Two pipelined executions, then error + ignored messages, then fresh Sync.
	pipeline := c.PgConn().StartPipeline(ctx)
	pipeline.SendQueryParams("SELECT 11", nil, nil, nil, nil)
	pipeline.SendQueryParams("SELECT 12", nil, nil, nil, nil)
	if e = pipeline.Sync(); e != nil {
		t.Fatal(e)
	}
	count := 0
	for {
		r, e := pipeline.GetResults()
		if e != nil {
			t.Fatal(e)
		}
		if r == nil {
			break
		}
		if rr, ok := r.(*pgconn.ResultReader); ok {
			if _, e = rr.Close(); e != nil {
				t.Fatal(e)
			}
		}
		count++
	}
	if e = pipeline.Close(); e != nil {
		t.Fatal(e)
	}
	if count < 3 {
		t.Fatal("pipeline responses missing", count)
	}
	// Exercise wire-level portal suspension and error recovery in a pipeline.
	wc, e := connect()
	if e != nil {
		t.Fatal(e)
	}
	defer wc.Close(ctx)
	wire := wc.PgConn().Frontend()
	readUntil := func(stop func(pgproto3.BackendMessage) bool) []pgproto3.BackendMessage {
		t.Helper()
		_ = wc.PgConn().Conn().SetReadDeadline(time.Now().Add(15 * time.Second))
		defer wc.PgConn().Conn().SetReadDeadline(time.Time{})
		messages := []pgproto3.BackendMessage{}
		for {
			m, err := wire.Receive()
			if err != nil {
				t.Fatal(err)
			}
			messages = append(messages, m)
			if stop(m) {
				return messages
			}
		}
	}
	wire.Send(&pgproto3.Parse{Name: "pages", Query: "SELECT id FROM items ORDER BY id"})
	wire.Send(&pgproto3.Bind{PreparedStatement: "pages", DestinationPortal: "page"})
	wire.Send(&pgproto3.Execute{Portal: "page", MaxRows: 1})
	wire.Send(&pgproto3.Flush{})
	if e = wire.Flush(); e != nil {
		t.Fatal(e)
	}
	readUntil(func(m pgproto3.BackendMessage) bool {
		if err, ok := m.(*pgproto3.ErrorResponse); ok {
			t.Fatal(err.Message)
		}
		_, ok := m.(*pgproto3.PortalSuspended)
		return ok
	})
	wire.Send(&pgproto3.Execute{Portal: "page"})
	wire.Send(&pgproto3.Sync{})
	if e = wire.Flush(); e != nil {
		t.Fatal(e)
	}
	readUntil(func(m pgproto3.BackendMessage) bool {
		if err, ok := m.(*pgproto3.ErrorResponse); ok {
			t.Fatal(err.Message)
		}
		_, ok := m.(*pgproto3.ReadyForQuery)
		return ok
	})
	wire.Send(&pgproto3.Parse{Query: "DELETE FROM items"})
	wire.Send(&pgproto3.Bind{})
	wire.Send(&pgproto3.Execute{})
	wire.Send(&pgproto3.Sync{})
	if e = wire.Flush(); e != nil {
		t.Fatal(e)
	}
	errorsSeen := 0
	readUntil(func(m pgproto3.BackendMessage) bool {
		if _, ok := m.(*pgproto3.ErrorResponse); ok {
			errorsSeen++
		}
		_, ok := m.(*pgproto3.ReadyForQuery)
		return ok
	})
	if errorsSeen != 1 {
		t.Fatal("pipeline rejection must emit one error", errorsSeen)
	}
	if e = wc.QueryRow(ctx, "SELECT 1").Scan(&n); e != nil {
		t.Fatal("pipeline Sync recovery", e)
	}
	// A mapped CancelRequest cancels only this connection, without exposing target keys.
	cancelResult := make(chan error, 1)
	go func() { _, e := sc.Exec(ctx, "SELECT pg_sleep(8)"); cancelResult <- e }()
	time.Sleep(150 * time.Millisecond)
	if e = sc.PgConn().CancelRequest(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-cancelResult:
		if e == nil {
			t.Fatal("cancel ineffective")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel timed out")
	}
	if e = sc.QueryRow(ctx, "SELECT 1").Scan(&n); e != nil {
		t.Fatal("cancel recovery", e)
	}
	// Bad password and unencrypted startup are rejected.
	limited, e := connect()
	if e != nil {
		t.Fatal(e)
	}
	rows, e := limited.Query(ctx, "SELECT generate_series(1,100001)")
	if e == nil {
		for rows.Next() {
		}
		e = rows.Err()
		rows.Close()
	}
	if e == nil {
		t.Fatal("row limit not enforced")
	}
	limited.Close(ctx)
	if e = c.QueryRow(ctx, "SELECT 1").Scan(&n); e != nil {
		t.Fatal("another session affected by result limit", e)
	}
	for _, params := range []map[string]string{{"options": "-c default_transaction_read_only=off"}, {"search_path": "public"}, {"role": "postgres"}, {"replication": "database"}, {"DateStyle": "SQL"}, {"TimeZone": "UTC'; DROP TABLE items; --"}} {
		badConfig := config.Copy()
		badConfig.RuntimeParams = params
		if bad, e := pgx.ConnectConfig(ctx, badConfig); e == nil {
			bad.Close(ctx)
			t.Fatal("unsafe startup accepted", params)
		}
	}
	wrong := config.Copy()
	wrong.Password = "wrong"
	if bad, e := pgx.ConnectConfig(ctx, wrong); e == nil {
		bad.Close(ctx)
		t.Fatal("bad password accepted")
	}
	plain := config.Copy()
	plain.TLSConfig = nil
	if bad, e := pgx.ConnectConfig(ctx, plain); e == nil {
		bad.Close(ctx)
		t.Fatal("plaintext accepted")
	}
	var leaked, completed, failed int
	e = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE parameter_meta::text LIKE '%binding-value-not-for-audit%' OR parameter_meta::text LIKE '%jdbc-秘密参数%'),count(*) FILTER(WHERE state='completed'),count(*) FILTER(WHERE state='failed') FROM database_sql_logs`).Scan(&leaked, &completed, &failed)
	if e != nil || leaked != 0 || completed < 4 || failed < 4 {
		t.Fatal("audit", leaked, completed, failed, e)
	}
	var typed int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM database_sql_logs WHERE command=69 AND sql_text='SELECT $1::text' AND parameter_meta->0->>'type'='25'`).Scan(&typed); e != nil || typed == 0 {
		t.Fatal("inferred parameter OID missing", typed, e)
	}
	t.Run("load-and-disconnect", func(t *testing.T) { postgresLoadAndDisconnect(t, pool, config) })
	// Revision limits apply to new connections; existing sessions retain their snapshot.
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	change, nextTag := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]int{"postgres-query-timeout-seconds": 1, "postgres-result-max-rows": 1, "postgres-result-max-mib": 1}}, admin)
	expect(t, change, 202)
	configured, e := connect()
	if e != nil {
		t.Fatal(e)
	}
	var timeoutValue string
	if e = configured.QueryRow(ctx, "SHOW statement_timeout").Scan(&timeoutValue); e != nil || timeoutValue != "1s" {
		t.Fatal("new timeout", timeoutValue, e)
	}
	if e = c.QueryRow(ctx, "SHOW statement_timeout").Scan(&timeoutValue); e != nil || timeoutValue != "10s" {
		t.Fatal("existing timeout changed", timeoutValue, e)
	}
	if _, e = configured.Exec(ctx, "SELECT pg_sleep(2)"); e == nil {
		t.Fatal("configured timeout not enforced")
	}
	rows, e = configured.Query(ctx, "SELECT id FROM items ORDER BY id")
	if e == nil {
		for rows.Next() {
		}
		e = rows.Err()
		rows.Close()
	}
	if e == nil {
		t.Fatal("configured row limit not enforced")
	}
	configured.Close(ctx)
	configured, e = connect()
	if e != nil {
		t.Fatal(e)
	}
	largeQuery := "SELECT string_agg('" + strings.Repeat("x", 100) + "','') FROM generate_series(1,11000)"
	var largeResult string
	if e = c.QueryRow(ctx, largeQuery).Scan(&largeResult); e != nil || len(largeResult) != 1100000 {
		t.Fatal("large response baseline", len(largeResult), e)
	}
	if _, e = configured.Exec(ctx, largeQuery); e == nil {
		t.Fatal("configured byte limit not enforced")
	}
	if _, denied := e.(*pgconn.PgError); denied {
		t.Fatal("byte limit test rejected for another reason", e)
	}
	configured.Close(ctx)
	rows, e = c.Query(ctx, "SELECT id FROM items ORDER BY id")
	if e != nil {
		t.Fatal(e)
	}
	count = 0
	for rows.Next() {
		count++
	}
	if e = rows.Err(); e != nil || count != 2 {
		t.Fatal("existing row limit changed", count, e)
	}
	rows.Close()
	change, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", nextTag, map[string]any{"set": map[string]int{"postgres-query-timeout-seconds": 10, "postgres-result-max-rows": 100000, "postgres-result-max-mib": 64}}, admin)
	expect(t, change, 202)
	// Rotation affects this disposable test user only, and invalidates open sockets.
	password = object(t, call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, reader, origin))["password"].(string)
	if _, e = c.Exec(ctx, "SELECT 1"); e == nil {
		t.Fatal("rotated password retained active access")
	}
	if bad, e := connect(); e == nil {
		bad.Close(ctx)
		t.Fatal("old password reconnects")
	}
	config.Password = password
	fresh, e := connect()
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close(ctx)
	idle, e := connect()
	if e != nil {
		t.Fatal(e)
	}
	defer idle.Close(ctx)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	if _, e = fresh.Exec(ctx, "SELECT 1"); e == nil {
		t.Fatal("revoked grant still executes")
	}
	if bad, e := connect(); e == nil {
		bad.Close(ctx)
		t.Fatal("revoked grant reconnects")
	}
	_ = idle.PgConn().Conn().SetReadDeadline(time.Now().Add(9 * time.Second))
	if _, e = idle.PgConn().Frontend().Receive(); e == nil {
		t.Fatal("idle revocation did not close socket")
	} else if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
		t.Fatal("idle revocation lease expired without closing")
	}
}
