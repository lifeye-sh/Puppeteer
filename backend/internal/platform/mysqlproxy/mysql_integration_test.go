package mysqlproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	"net"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/database"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMySQL84TLSRelay(t *testing.T) {
	directory := os.Getenv("NT_R12_MYSQL_FIXTURE")
	if directory == "" {
		t.Skip("isolated MySQL fixture not configured")
	}
	credentials, e := os.ReadFile(filepath.Join(directory, "credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	var passwords map[string]string
	if e = json.Unmarshal(credentials, &passwords); e != nil {
		t.Fatal(e)
	}
	clear(credentials)
	ca, e := os.ReadFile(filepath.Join(directory, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	target := Target{Address: "nt-r12-mysql-fixture:3306", User: "reader", Password: passwords["reader"], Database: "inventory", ServerName: "nt-r12-mysql-fixture", CA: string(ca), Timeout: 10 * time.Second}
	upstream, e := ConnectTarget(context.Background(), target, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = upstream.Execute("INSERT INTO items VALUES(99,'forbidden',NULL)"); e == nil {
		t.Fatal("read-only target can write")
	}
	upstream.Close()
	writer := target
	writer.User = "writer"
	writer.Password = passwords["writer"]
	if c, e := ConnectTarget(context.Background(), writer, true); e == nil {
		c.Close()
		t.Fatal("write-capable target accepted as readonly")
	}

	writer.ProgramPermissions = true
	programReader, err := ConnectTarget(context.Background(), writer, true)
	if err != nil {
		t.Fatal("program mode rejected broader account", err)
	}
	if _, err = programReader.Execute("INSERT INTO items VALUES(99,'forbidden',NULL)"); err == nil {
		programReader.Close()
		t.Fatal("program readonly connection allowed write")
	}
	programReader.Close()
	programWriter, err := ConnectDMLTarget(context.Background(), writer)
	if err != nil {
		t.Fatal("program DML rejected broader account", err)
	}
	programWriter.Close()
	wrongName := target
	wrongName.ServerName = "wrong.internal"
	if c, e := ConnectTarget(context.Background(), wrongName, true); e == nil {
		c.Close()
		t.Fatal("TLS name mismatch accepted")
	}
	certificates := httptest.NewTLSServer(nil)
	defer certificates.Close()
	roots := x509.NewCertPool()
	roots.AddCert(certificates.Certificate())
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	policy, e := database.NewSQLPolicy()
	if e != nil {
		t.Fatal(e)
	}
	secret := strings.Repeat("A", 43)
	var auditStarts, auditFinishes atomic.Int32
	protocol := NewServer(certificates.TLS, func(_ context.Context, user, password string) error {
		if user != "reader@inventory" || password != secret {
			return errors.New("denied")
		}
		return nil
	})
	ready := make(chan error, 1)
	go func() {
		raw, e := listener.Accept()
		if e != nil {
			ready <- e
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(30 * time.Second))
		var remote *client.Conn
		downstream, e := protocol.NewCustomizedConn(raw, &IdentityHandler{Ready: func(*server.Conn) error {
			var e error
			remote, e = ConnectTarget(context.Background(), target, true)
			return e
		}}, server.EmptyHandler{})
		if e != nil {
			ready <- e
			return
		}
		defer downstream.Close()
		defer remote.Close()
		relay := Relay{Downstream: downstream.Conn.Conn, Upstream: remote.Conn.Conn, Database: "inventory", Check: func(context.Context) error { return nil }, Validate: policy.ReadOnly, Audit: func(_ context.Context, event QueryEvent) error {
			if event.Phase == "start" {
				auditStarts.Add(1)
			} else {
				auditFinishes.Add(1)
			}
			return nil
		}}
		ready <- relay.Serve(context.Background())
	}()
	connection, e := client.Connect(listener.Addr().String(), "reader@inventory", secret, "inventory", func(c *client.Conn) error {
		c.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "example.com"})
		c.UnsetCapability(mysql.CLIENT_DEPRECATE_EOF | mysql.CLIENT_QUERY_ATTRIBUTES)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	if e = connection.Ping(); e != nil {
		t.Fatal(e)
	}
	result, e := connection.Execute("SELECT label FROM items ORDER BY id")
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Values) != 2 {
		t.Fatal("unexpected rows")
	}
	null, e := result.IsNull(1, 0)
	if e != nil || !null {
		t.Fatal("NULL not forwarded", e)
	}
	statement, e := connection.Prepare("SELECT label FROM items WHERE id=?")
	if e != nil {
		t.Fatal(e)
	}
	result, e = statement.Execute(1)
	if e != nil {
		t.Fatal(e)
	}
	value, e := result.GetString(0, 0)
	if e != nil || value != "R12 fixture" {
		t.Fatal(value, e)
	}
	statement.Close()
	for _, query := range []string{"DELETE FROM items", "SELECT SLEEP(10)", "SELECT 1; DELETE FROM items", "SELECT LOAD_FILE('/etc/passwd')"} {
		if _, e = connection.Execute(query); e == nil {
			t.Fatal("unsafe query accepted", query)
		}
	}
	if _, e = connection.Execute("SELECT count(*) FROM items"); e != nil {
		t.Fatal("denied command desynchronized connection", e)
	}
	var streamed int
	var streamingResult mysql.Result
	e = connection.ExecuteSelectStreaming("SELECT repeat('x',1000) FROM items", &streamingResult, func([]mysql.FieldValue) error { streamed++; return nil }, nil)
	if e != nil || streamed != 2 {
		t.Fatal("streaming", e, streamed)
	}
	if auditStarts.Load() < 9 || auditFinishes.Load() != auditStarts.Load() {
		t.Fatal("missing paired audit records", auditStarts.Load(), auditFinishes.Load())
	}
	connection.Close()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not stop")
	}
}
