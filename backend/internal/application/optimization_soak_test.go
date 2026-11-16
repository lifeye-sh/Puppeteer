package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"puppeteer/backend/internal/modules/backup"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/guac"
	"puppeteer/backend/internal/platform/resourcebudget"
	"puppeteer/backend/migrations"
)

// Opt-in sustained real PostgreSQL/SSH/guacd/FFmpeg exercise. The VNC target is
// a diagnostic color fixture, not a Windows desktop. Never uses business data.
func TestOptimizationSoak(t *testing.T) {
	seconds, _ := strconv.Atoi(os.Getenv("NT_OPTIMIZATION_SOAK_SECONDS"))
	if seconds < 60 {
		t.Skip("explicit soak duration >= 60 seconds required")
	}
	if os.Getenv("NT_SOAK_ISOLATED") != "1" {
		t.Fatal("disposable environment required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The runner supplies a dedicated disposable database. Use the production
	// public schema: backup snapshots intentionally do not inherit a test schema.
	config, err := pgxpool.ParseConfig(os.Getenv("NT_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid isolated database configuration")
	}
	budget, err := resourcebudget.Database("light")
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns, config.MinConns = budget.PoolMax, budget.PoolMin
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer pool.Close()
	var empty bool
	if err = pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public')`).Scan(&empty); err != nil || !empty {
		t.Fatal("soak requires an empty disposable database")
	}
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	a := New(pool, Config{AllowedOrigins: []string{origin}, VaultKey: bytes.Repeat([]byte{43}, 32), EncryptionKey: bytes.Repeat([]byte{42}, 32), ResourceProfile: "light", MP4URL: os.Getenv("NT_MP4_TEST_URL"), MP4Token: os.Getenv("NT_MP4_TEST_TOKEN"), MP4Directory: t.TempDir(), GuacdAddr: "guacd:4822", GuacdRelayHost: "load", BackupDirectory: t.TempDir(), DatabaseURL: os.Getenv("NT_TEST_DATABASE_URL")})
	if err = a.system.CheckMFAKey(ctx, identity.Digest(string(a.config.EncryptionKey))); err != nil {
		t.Fatal(err)
	}
	if err = a.system.CheckVaultKey(ctx, identity.Digest(string(a.config.VaultKey))); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	sshAsset := r04Asset(t, srv, admin, host, port, true)
	vnc := exec.CommandContext(ctx, "/app/soak-vnc")
	if err := vnc.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vnc.Process.Kill(); _ = vnc.Wait() }()
	credential := postID(t, srv, "/api/admin/credentials", credInput("soak graphics"), admin)
	input := assetInput("soak VNC", credential, "")
	input["host"] = "127.0.0.1"
	input["port"] = 5900
	input["protocol"] = "vnc"
	graphicsAsset := postID(t, srv, "/api/admin/assets", input, admin)
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	bs := backup.New(a.db)
	if err = bs.SaveConfig(ctx, backup.Config{Recipient: key.Recipient().String(), RetentionDays: 1, ScheduleTime: "02:00", Timezone: "UTC", Scope: "full", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); a.RunRecordingExports(ctx) }()
	go func() { defer workers.Done(); a.RunTaskMaintenance(ctx) }()
	defer func() { cancel(); workers.Wait() }()
	sqlErrors := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				var n int
				err := a.db.Row(ctx, `SELECT count(*) FROM sessions WHERE state='connected'`).Scan(&n)
				if err != nil && ctx.Err() == nil {
					select {
					case sqlErrors <- err:
					default:
					}
					return
				}
			}
		}
	}()
	report := func(v any) { data, _ := json.Marshal(v); t.Log("SOAK " + string(data)) }
	convert := func(id string) {
		path := "/api/sessions/" + id + "/recording/mp4"
		expect(t, call(t, srv.URL, "POST", path, nil, admin, origin), 202)
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			state, e := a.audit.Export(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if state.State == "failed" {
				t.Fatal("soak conversion failed", state.Reason)
			}
			if state.State == "succeeded" {
				result := call(t, srv.URL, "GET", path+"/download", nil, admin, origin)
				expect(t, result, 200)
				if len(result.body) < 1000 || string(result.body[4:8]) != "ftyp" {
					t.Fatal("invalid video")
				}
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("soak conversion deadline")
	}
	started := time.Now()
	cycles, backups := 0, 0
	for time.Since(started) < time.Duration(seconds)*time.Second {
		cycleStart := time.Now()
		// Scope helper cleanups to this cycle. Otherwise attach() keeps every
		// closed websocket and its buffers reachable until the entire soak ends.
		func() {
			if cycles%30 == 0 {
				admin = loginAs(t, srv, "accept-admin")
			}
			id, ticket := reserve(t, srv, admin, sshAsset, false)
			ws := attachOwned(t, srv, admin, id, ticket)
			defer ws.Close()
			readUntil(t, ws, "SSH FIXTURE READY")
			if err = ws.WriteMessage(websocket.TextMessage, []byte("1soak-check\n")); err != nil {
				t.Fatal(err)
			}
			readUntil(t, ws, "EXECUTED soak-check")
			ws.Close()
			waitClosed(t, a, id)
			graphicsID, graphicsTicket := reserve(t, srv, admin, graphicsAsset, false)
			display := attachOwned(t, srv, admin, graphicsID, graphicsTicket)
			defer display.Close()
			frames := 0
			until := time.Now().Add(5 * time.Second)
			for time.Now().Before(until) || frames == 0 {
				display.SetReadDeadline(time.Now().Add(15 * time.Second))
				_, data, e := display.ReadMessage()
				if e != nil {
					t.Fatal(e)
				}
				if len(data) == 0 {
					continue
				}
				if data[0] == '0' {
					t.Fatal("graphics fixture rejected", string(data[1:]))
				}
				if data[0] != '7' {
					continue
				}
				raw, e := base64.StdEncoding.DecodeString(string(data[1:]))
				if e != nil {
					t.Fatal(e)
				}
				reader := guac.NewReader(strings.NewReader(string(raw)))
				for {
					p, e := reader.Read()
					if e == io.EOF {
						break
					}
					if e != nil {
						t.Fatal(e)
					}
					if p[0] == "sync" {
						frames++
						time.Sleep(100 * time.Millisecond)
						for _, wire := range []string{guac.Encode("sync", p[1]), guac.Encode("mouse", "10", "10", strconv.Itoa(frames%2))} {
							if e = display.WriteMessage(websocket.TextMessage, []byte("7"+base64.StdEncoding.EncodeToString([]byte(wire)))); e != nil {
								t.Fatal(e)
							}
						}
					}
				}
			}
			display.Close()
			waitClosed(t, a, graphicsID)
			convert(id)
			convert(graphicsID)
			if cycles%60 == 0 {
				requestKey := fmt.Sprintf("10000000-0000-4000-8000-%012d", cycles+1)
				result := call(t, srv.URL, "POST", "/api/admin/backup/tasks", map[string]any{"requestKey": requestKey, "drainConfirmed": true}, admin, origin)
				expect(t, result, 202)
				jobID := object(t, result)["id"].(string)
				if err = a.RunBackupOnce(ctx); err != nil {
					t.Fatal("backup", err)
				}
				task, e := bs.Task(ctx, jobID)
				if e != nil || task.State != "succeeded" {
					t.Fatal("backup incomplete", task.State, e)
				}
				backups++
			}
			select {
			case e := <-sqlErrors:
				t.Fatal("concurrent SQL", e)
			default:
			}
			cycles++
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			stats := a.db.PoolStats()
			report(map[string]any{"cycles": cycles, "backups": backups, "elapsedSeconds": int(time.Since(started).Seconds()), "cycleMS": time.Since(cycleStart).Milliseconds(), "frames": frames, "poolMax": stats.MaxConns(), "poolTotal": stats.TotalConns(), "poolWaitNS": stats.EmptyAcquireWaitTime().Nanoseconds(), "poolCanceled": stats.CanceledAcquireCount(), "heapBytes": memory.HeapAlloc, "heapInuseBytes": memory.HeapInuse, "heapSysBytes": memory.HeapSys, "heapObjects": memory.HeapObjects, "gcCount": memory.NumGC, "goroutines": runtime.NumGoroutine()})
		}()
		remaining := min(time.Until(cycleStart.Add(time.Minute)), time.Until(started.Add(time.Duration(seconds)*time.Second)))
		if remaining > 0 {
			time.Sleep(remaining)
		}
	}
	report(map[string]any{"complete": true, "cycles": cycles, "backups": backups, "elapsedSeconds": int(time.Since(started).Seconds())})
}
