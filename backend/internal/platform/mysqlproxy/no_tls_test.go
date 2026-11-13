package mysqlproxy

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExplicitNoTLSMySQL(t *testing.T) {
	if os.Getenv("NT_TEST_NO_TLS") != "1" {
		t.Skip("isolated no-TLS fixture required")
	}
	target := Target{Address: "puppeteer-no-tls-mysql:3306", User: "reader", Password: "fixture-reader-only", Database: "inventory", TLSMode: "disable", Timeout: 5 * time.Second}
	ctx := context.Background()
	c, e := ConnectTarget(ctx, target, true)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	rows, e := c.Execute("SELECT id,label FROM items")
	if e != nil || rows.RowNumber() != 1 {
		t.Fatal("query failed", e)
	}
	state, e := c.Execute("SHOW SESSION STATUS LIKE 'Ssl_cipher'")
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := state.GetString(0, 1)
	if e != nil || cipher != "" {
		t.Fatal("unexpected TLS", e)
	}
	if _, e = c.Execute("INSERT INTO items VALUES(2,'denied')"); e == nil {
		t.Fatal("readonly accepted write")
	}
	target.User = "writer"
	target.Password = "fixture-writer-only"
	if c, e := ConnectTarget(ctx, target, true); e == nil {
		c.Close()
		t.Fatal("write role accepted as readonly")
	}
	writer, e := ConnectDMLTarget(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	if _, e = writer.Execute("START TRANSACTION"); e != nil {
		t.Fatal(e)
	}
	if _, e = writer.Execute("INSERT INTO items VALUES(2,'rollback')"); e != nil {
		t.Fatal(e)
	}
	if _, e = writer.Execute("ROLLBACK"); e != nil {
		t.Fatal(e)
	}
	target.TLSMode = ""
	if c, e := ConnectTarget(ctx, target, true); e == nil {
		c.Close()
		t.Fatal("default downgraded")
	}
	target.TLSMode = "prefer"
	if c, e := ConnectTarget(ctx, target, true); e == nil {
		c.Close()
		t.Fatal("invalid mode accepted")
	}
}
