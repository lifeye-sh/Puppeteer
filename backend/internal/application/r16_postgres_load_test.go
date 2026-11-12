package application

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the real TLS/SCRAM proxy and PostgreSQL fixture, including audit
// finalization. This is a bounded regression workload, not a capacity benchmark.
func postgresLoadAndDisconnect(t *testing.T, pool *pgxpool.Pool, config *pgx.ConnConfig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	open := func(t *testing.T) *pgx.Conn {
		t.Helper()
		c, err := pgx.ConnectConfig(ctx, config.Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.PgConn().Conn().Close() })
		return c
	}
	sessionID := func(t *testing.T, c *pgx.Conn) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `SELECT id FROM database_sessions WHERE remote_address=$1 AND ended_at IS NULL`, c.PgConn().Conn().LocalAddr().String()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	wait := func(t *testing.T, description string, condition func() (bool, error)) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			ok, err := condition()
			if err != nil {
				t.Fatal(description, err)
			}
			if ok {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
		t.Fatal("timed out:", description)
	}
	closed := func(t *testing.T, id string) {
		t.Helper()
		wait(t, "session and running SQL finalized", func() (bool, error) {
			var done bool
			err := pool.QueryRow(ctx, `SELECT ended_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM database_sql_logs WHERE session_id=$1 AND (state='running' OR finished_at IS NULL)) FROM database_sessions WHERE id=$1`, id).Scan(&done)
			return done, err
		})
	}
	t.Run("sixteen-connections-320-bound-queries", func(t *testing.T) {
		const clients, queries = 16, 20
		connections := make([]*pgx.Conn, clients)
		ids := make([]string, clients)
		for i := range connections {
			connections[i] = open(t)
			ids[i] = sessionID(t, connections[i])
		}
		var wg sync.WaitGroup
		errors := make(chan error, clients)
		start := make(chan struct{})
		for i, c := range connections {
			wg.Add(1)
			go func(i int, c *pgx.Conn) {
				defer wg.Done()
				<-start
				for j := 0; j < queries; j++ {
					want := fmt.Sprintf("load-private-%d-%d", i, j)
					var got string
					if err := c.QueryRow(ctx, "SELECT $1::text", want).Scan(&got); err != nil {
						errors <- err
						return
					}
					if got != want {
						errors <- fmt.Errorf("cross-connection result mismatch")
						return
					}
				}
			}(i, c)
		}
		close(start)
		wg.Wait()
		close(errors)
		for err := range errors {
			t.Error(err)
		}
		for i, c := range connections {
			_ = c.Close(ctx)
			closed(t, ids[i])
		}
		var completed, leaked int
		if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE command=69 AND state='completed'),count(*) FILTER(WHERE parameter_meta::text LIKE '%load-private-%') FROM database_sql_logs WHERE session_id=ANY($1::uuid[])`, ids).Scan(&completed, &leaked); err != nil {
			t.Fatal(err)
		}
		if completed != clients*queries || leaked != 0 {
			t.Fatal("load audit", completed, leaked)
		}
	})
	for _, mode := range []string{"idle", "in-flight-query", "copy-stream"} {
		t.Run("client-disconnect-"+mode, func(t *testing.T) {
			c := open(t)
			id := sessionID(t, c)
			if mode != "idle" {
				sql := "SELECT pg_sleep(8)"
				if mode == "copy-stream" {
					sql = "COPY (SELECT generate_series(1,100000)) TO STDOUT"
				}
				// Send directly so the test can close the wire during a response,
				// without racing pgx's protocol methods on the same connection.
				front := c.PgConn().Frontend()
				front.Send(&pgproto3.Query{String: sql})
				if err := front.Flush(); err != nil {
					t.Fatal(err)
				}
				if mode == "copy-stream" {
					if err := c.PgConn().Conn().SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
						t.Fatal(err)
					}
					if message, err := front.Receive(); err != nil {
						t.Fatal(err)
					} else if _, ok := message.(*pgproto3.CopyOutResponse); !ok {
						t.Fatalf("expected COPY response, got %T", message)
					}
				} else {
					wait(t, "query admitted", func() (bool, error) {
						var yes bool
						err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_sql_logs WHERE session_id=$1 AND state='running')`, id).Scan(&yes)
						return yes, err
					})
				}
			}
			_ = c.PgConn().Conn().Close()
			closed(t, id)
			if mode == "in-flight-query" {
				var state string
				if err := pool.QueryRow(ctx, `SELECT state FROM database_sql_logs WHERE session_id=$1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&state); err != nil || state != "unknown" {
					t.Fatal("interrupted query must not report success", state, err)
				}
			}
			healthy := open(t)
			var n int
			if err := healthy.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
				t.Fatal("subsequent connection", n, err)
			}
			_ = healthy.Close(ctx)
		})
	}
	t.Run("target-disconnect", func(t *testing.T) {
		// Only the dedicated fixture's explicitly supplied administrator DSN is
		// allowed. Terminate one exact reader PID, never the fixture or platform.
		adminConfig, err := pgx.ParseConfig(os.Getenv("NT_TEST_DATABASE_URL"))
		if err != nil || adminConfig.Host != "r16-postgres-fixture" {
			t.Fatal("isolated fixture DSN required")
		}
		admin, err := pgx.ConnectConfig(ctx, adminConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close(ctx)
		c := open(t)
		id := sessionID(t, c)
		var pid int32
		if err = c.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		var terminated bool
		if err = admin.QueryRow(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid=$1 AND usename='reader' AND datname='inventory'`, pid).Scan(&terminated); err != nil || !terminated {
			t.Fatal("target termination", terminated, err)
		}
		if _, err = c.Exec(ctx, "SELECT 1"); err == nil {
			t.Fatal("dead target query succeeded")
		}
		_ = c.PgConn().Conn().Close()
		closed(t, id)
		healthy := open(t)
		if _, err = healthy.Exec(ctx, "SELECT 1"); err != nil {
			t.Fatal("healthy target connection", err)
		}
		_ = healthy.Close(ctx)
	})
}
