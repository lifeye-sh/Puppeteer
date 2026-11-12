package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestR12PlatformMySQLRevocationAndAudit(t *testing.T) {
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
	pool, a, srv := r02Server(t, false)
	a.config.DatabaseProxyName = "localhost"
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("sql-reader", nil, nil), admin)
	reader := loginAs(t, srv, "sql-reader")
	credential := postID(t, srv, "/api/admin/credentials", map[string]any{"name": "R12 actual readonly", "kind": "password", "username": "reader", "secret": map[string]string{"password": secrets["reader"]}}, admin)
	asset := postID(t, srv, "/api/admin/database-assets", map[string]any{"name": "R12 actual MySQL", "alias": "inventory", "host": "nt-r12-mysql-fixture", "port": 3306, "database": "inventory", "readonlyCredentialId": credential, "tlsServerName": "nt-r12-mysql-fixture", "tlsCa": string(ca), "enabled": true, "connectTimeout": 10}, admin)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	passwordResult := call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, reader, origin)
	expect(t, passwordResult, 200)
	password := object(t, passwordResult)["password"].(string)
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
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			workers.Add(1)
			go func() { defer workers.Done(); a.databaseConnection(ctx, raw, tlsConfig) }()
		}
	}()
	defer func() { listener.Close(); <-done; cancel(); workers.Wait() }()
	connect := func(secret, db string) (*client.Conn, error) {
		return client.Connect(listener.Addr().String(), "sql-reader@inventory", secret, db, func(c *client.Conn) error {
			c.SetTLSConfig(&tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			c.UnsetCapability(mysql.CLIENT_DEPRECATE_EOF | mysql.CLIENT_QUERY_ATTRIBUTES)
			return nil
		})
	}
	if c, e := connect(password, "mysql"); e == nil {
		c.Close()
		t.Fatal("unauthorized initial database accepted")
	}
	connection, e := connect(password, "inventory")
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	if _, e = connection.Execute("SELECT count(*) FROM items"); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	if e = connection.Ping(); e == nil {
		t.Fatal("revoked grant still permits commands")
	}
	if c, e := connect(password, "inventory"); e == nil {
		c.Close()
		t.Fatal("revoked grant permits reconnect")
	}
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	connection, e = connect(password, "inventory")
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	rotation := call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, reader, origin)
	expect(t, rotation, 200)
	if e = connection.Ping(); e == nil {
		t.Fatal("rotated credential kept live connection")
	}
	if c, e := connect(password, "inventory"); e == nil {
		c.Close()
		t.Fatal("old password accepted")
	}
	current := object(t, rotation)["password"].(string)
	connection, e = connect(current, "inventory")
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	if _, e = connection.Execute("DELETE FROM items"); e == nil {
		t.Fatal("write accepted")
	}
	var completed, failed int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE state='completed'),count(*) FILTER(WHERE state='failed') FROM database_sql_logs`).Scan(&completed, &failed); e != nil || completed < 1 || failed < 1 {
		t.Fatal("missing SQL audit", completed, failed, e)
	}
	// Revocation also expires an idle connection, without waiting for a command.
	expect(t, call(t, srv.URL, "DELETE", "/api/account/database-password", map[string]any{"password": testPassword}, reader, origin), 204)
	deadline := time.Now().Add(8 * time.Second)
	for {
		var active int
		if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM database_sessions WHERE ended_at IS NULL`).Scan(&active); e != nil {
			t.Fatal(e)
		}
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle lease did not expire")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
