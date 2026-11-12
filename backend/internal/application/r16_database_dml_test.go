package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/mysqlproxy"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestR16NativeDatabaseDML(t *testing.T) {
	dir := os.Getenv("NT_R12_MYSQL_FIXTURE")
	if dir == "" {
		t.Skip("isolated MySQL fixture required")
	}
	read := func(name string, out any) {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		defer clear(b)
		if e = json.Unmarshal(b, out); e != nil {
			t.Fatal(e)
		}
	}
	var secrets, dml map[string]string
	read("credentials.json", &secrets)
	read("native-dml.json", &dml)
	ca, e := os.ReadFile(filepath.Join(dir, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	endpoint := mysqlproxy.Target{Address: "nt-r12-mysql-fixture:3306", User: "writer", Password: secrets["writer"], Database: "inventory", ServerName: "nt-r12-mysql-fixture", CA: string(ca), Timeout: 10 * time.Second}
	if c, e := mysqlproxy.ConnectDMLTarget(ctx, endpoint); e == nil {
		c.Close()
		t.Fatal("DDL-capable credential accepted as native DML")
	}
	direct, e := mysqlproxy.ConnectTarget(ctx, endpoint, false)
	if e != nil {
		t.Fatal(e)
	}
	defer direct.Close()
	// Separate table, leaving shared R12 fixture rows unchanged.
	table := "r16_native_dml_test"
	if _, e = direct.Execute("CREATE TABLE IF NOT EXISTS " + table + "(id INT PRIMARY KEY,label VARCHAR(100)) ENGINE=InnoDB"); e != nil {
		t.Fatal(e)
	}
	defer direct.Execute("DROP TABLE " + table)
	if _, e = direct.Execute("DELETE FROM " + table); e != nil {
		t.Fatal(e)
	}
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "native dml", "permissions": []string{"database.write"}}, admin)
	writerID := postID(t, srv, "/api/admin/users", userInput("native-writer", []string{role}, nil), admin)
	readerID := postID(t, srv, "/api/admin/users", userInput("native-reader", nil, nil), admin)
	writerCookie := loginAs(t, srv, "native-writer")
	readerCookie := loginAs(t, srv, "native-reader")
	credential := func(name, user, pw string) string {
		return postID(t, srv, "/api/admin/credentials", map[string]any{"name": name, "kind": "password", "username": user, "secret": map[string]string{"password": pw}}, admin)
	}
	readID := credential("DML read fixture", "reader", secrets["reader"])
	writeID := credential("DML fixture", dml["username"], dml["password"])
	body := map[string]any{"name": "DML fixture", "alias": "native_dml", "host": "nt-r12-mysql-fixture", "port": 3306, "database": "inventory", "readonlyCredentialId": readID, "nativeWriteCredentialId": writeID, "tlsServerName": "nt-r12-mysql-fixture", "tlsCa": string(ca), "enabled": true, "connectTimeout": 10}
	asset := postID(t, srv, "/api/admin/database-assets", body, admin)
	for _, id := range []string{writerID, readerID} {
		postID(t, srv, "/api/admin/grants", map[string]any{"userId": id, "resourceId": asset}, admin)
	}
	password := func(cookie *http.Cookie) string {
		r := call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, cookie, origin)
		expect(t, r, 200)
		return object(t, r)["password"].(string)
	}
	writerPW, readerPW := password(writerCookie), password(readerCookie)
	tlsConfig, e := a.databaseProxyTLS(ctx)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(tlsConfig.Certificates[0].Leaf)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			raw, e := ln.Accept()
			if e != nil {
				return
			}
			workers.Add(1)
			go func() { defer workers.Done(); a.databaseConnection(ctx, raw, tlsConfig) }()
		}
	}()
	defer func() { ln.Close(); <-done; cancel(); workers.Wait() }()
	connect := func(user, pw string) *client.Conn {
		t.Helper()
		c, e := client.Connect(ln.Addr().String(), user+"@native_dml", pw, "inventory", func(c *client.Conn) error {
			c.SetTLSConfig(&tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			c.UnsetCapability(mysql.CLIENT_DEPRECATE_EOF | mysql.CLIENT_QUERY_ATTRIBUTES)
			c.ReadTimeout = 8 * time.Second
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	exec := func(c *client.Conn, q string) {
		t.Helper()
		if _, e := c.Execute(q); e != nil {
			t.Fatal(q, e)
		}
	}
	deny := func(c *client.Conn, q string) {
		t.Helper()
		if _, e := c.Execute(q); e == nil {
			t.Fatal("accepted forbidden SQL", q)
		}
	}
	save := func(block bool, pw string, want int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.DatabaseBlockDMLKey: block}, "password": pw}, admin)
		expect(t, r, want)
	}
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	initial := connect("native-writer", writerPW)
	deny(initial, "INSERT INTO "+table+" VALUES (1,'default-denied')")
	if e = a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{system.DatabaseBlockDMLKey: json.RawMessage("false")}, "")
		return e
	}); !errors.Is(e, fault.Denied) {
		t.Fatal("configuration proof bypass", e)
	}
	save(false, "", 422)
	apply("dml-a")
	apply("dml-b")
	save(false, testPassword, 202)
	apply("dml-a")
	waiting := connect("native-writer", writerPW)
	deny(waiting, "INSERT INTO "+table+" VALUES (1,'before-ack')")
	waiting.Close()
	apply("dml-b")
	deny(initial, "INSERT INTO "+table+" VALUES (1,'no-session-upgrade')")
	reader := connect("native-reader", readerPW)
	deny(reader, "INSERT INTO "+table+" VALUES (1,'no-role')")
	c := connect("native-writer", writerPW)
	exec(c, "INSERT INTO "+table+" VALUES (1,'DML fixture')")
	stmt, e := c.Prepare("UPDATE " + table + " SET label=? WHERE id=?")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = stmt.Execute("bound secret omitted from audit", 1); e != nil {
		t.Fatal(e)
	}
	stmt.Close()
	exec(c, "BEGIN")
	exec(c, "INSERT INTO "+table+" VALUES (2,'rollback')")
	exec(c, "ROLLBACK")
	exec(c, "BEGIN")
	exec(c, "INSERT INTO "+table+" VALUES (3,'commit')")
	exec(c, "COMMIT")
	for _, q := range []string{"DROP TABLE " + table, "CALL evil()", "UPDATE mysql.user SET User='bad'", "UPDATE " + table + " SET label=load_file('/etc/passwd')", "INSERT INTO " + table + " VALUES (4,'a');DELETE FROM " + table} {
		deny(c, q)
	}
	r := call(t, srv.URL, "GET", "/api/portal/database-assets", nil, writerCookie, origin)
	expect(t, r, 200)
	items := object(t, r)["items"].([]any)
	item := items[0].(map[string]any)
	if item["nativeWriteAvailable"] != true || item["nativeWriteCredentialId"] != "" {
		t.Fatal("portal permission or secret projection", item["nativeWriteAvailable"])
	}
	// Revocation must abort an uncommitted transaction, without upgrading readers.
	exec(c, "BEGIN")
	exec(c, "INSERT INTO "+table+" VALUES (4,'revoked')")
	expect(t, call(t, srv.URL, "PUT", "/api/admin/roles/"+role, map[string]any{"name": "native dml", "version": 1, "permissions": []string{}}, admin, origin), 200)
	deny(c, "COMMIT")
	c.Close()
	expect(t, call(t, srv.URL, "PUT", "/api/admin/roles/"+role, map[string]any{"name": "native dml", "version": 2, "permissions": []string{"database.write"}}, admin, origin), 200)
	c = connect("native-writer", writerPW)
	exec(c, "BEGIN")
	exec(c, "INSERT INTO "+table+" VALUES (5,'blocked')")
	save(true, testPassword, 202)
	deny(c, "COMMIT")
	c.Close()
	apply("dml-a")
	apply("dml-b")
	save(false, testPassword, 202)
	apply("dml-a")
	apply("dml-b")
	c = connect("native-writer", writerPW)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/credentials/"+writeID, map[string]any{"name": "DML fixture", "kind": "password", "username": dml["username"], "version": 1, "secret": map[string]string{"password": dml["password"]}}, admin, origin), 200)
	deny(c, "SELECT 1")
	c.Close()
	rows, e := direct.Execute("SELECT id FROM " + table + " ORDER BY id")
	if e != nil || len(rows.Values) != 2 {
		t.Fatal("transaction rollback/commit", e)
	}
	for i, id := range []int64{1, 3} {
		actual, _ := rows.GetInt(i, 0)
		if actual != id {
			t.Fatal("unexpected committed row", actual)
		}
	}
	var profiles, audits, leaked int
	var affected int64
	if e = pool.QueryRow(ctx, `SELECT coalesce(sum(row_count),0) FROM database_sql_logs WHERE command=23 AND sql_text LIKE 'UPDATE r16_native_dml_test%'`).Scan(&affected); e != nil || affected != 1 {
		t.Fatal("affected rows not audited", affected, e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM database_sessions WHERE access_profile='dml'`).Scan(&profiles); e != nil || profiles < 3 {
		t.Fatal(profiles, e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE parameter_meta::text LIKE '%bound secret%') FROM database_sql_logs WHERE sql_text LIKE '%r16_native_dml_test%'`).Scan(&audits, &leaked); e != nil || audits < 10 || leaked != 0 {
		t.Fatal(audits, leaked, e)
	}
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := a.system.Configuration(ctx, 0)
	if e != nil || !v.Values.Bool(system.DatabaseBlockDMLKey) {
		t.Fatal("restore must block DML", e)
	}
}

func TestR16DatabaseDMLConfigurationMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.DatabaseBlockDMLKey: false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
