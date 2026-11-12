package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"filippo.io/age"
	"golang.org/x/net/webdav"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/backup"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestR11RemoteRetryAndRetention(t *testing.T) {
	p, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	dir, remoteDir := t.TempDir(), t.TempDir()
	a.config.BackupDirectory = dir
	a.config.DatabaseURL = p.Config().ConnString()
	var failing atomic.Bool
	handler := &webdav.Handler{FileSystem: webdav.Dir(remoteDir), LockSystem: webdav.NewMemLS()}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "fixture" || p != "remote-only-secret" {
			w.WriteHeader(401)
			return
		}
		if failing.Load() {
			w.WriteHeader(503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	input := map[string]any{"name": "WebDAV fixture", "kind": "webdav", "endpoint": remote.URL, "username": "fixture", "secret": "remote-only-secret", "enabled": true, "allowHttp": true, "password": testPassword}
	id := object(t, call(t, srv.URL, "POST", "/api/admin/backup/destinations", input, admin, origin))["id"].(string)
	listed := call(t, srv.URL, "GET", "/api/admin/backup/destinations", nil, admin, origin)
	expect(t, listed, 200)
	if bytes.Contains(listed.body, []byte("remote-only-secret")) {
		t.Fatal("remote secret exposed")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/backup/destinations/"+id+"/test", map[string]any{}, admin, origin), 200)
	key, _ := age.GenerateX25519Identity()
	cfg := backup.Config{Recipient: key.Recipient().String(), Scope: "full", RetentionDays: 31, ScheduleTime: "02:00", Timezone: "Asia/Shanghai", Revision: 1}
	s := backup.New(a.db)
	if e := s.SaveConfig(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	job, e := s.Request(ctx, "", taskUUID(110))
	if e != nil {
		t.Fatal(e)
	}
	var encrypted bytes.Buffer
	writer, e := age.Encrypt(&encrypted, key.Recipient())
	if e != nil {
		t.Fatal(e)
	}
	writer.Write([]byte("isolated remote copy fixture"))
	writer.Close()
	payload := encrypted.Bytes()
	sum := sha256.Sum256(payload)
	file := filepath.Join(dir, job+".ntbackup.age")
	if e = os.WriteFile(file, payload, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO backup_files(id,storage_key,size,sha256,manifest) VALUES($1,$2,$3,$4,'{}');`, job, job+".ntbackup.age", len(payload), hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE jobs SET state='partial' WHERE id=$1`, job); e != nil {
		t.Fatal(e)
	}
	retry := func() {
		expect(t, call(t, srv.URL, "POST", "/api/admin/backup/tasks/"+job+"/retry", map[string]any{}, admin, origin), 204)
	}
	failing.Store(true)
	retry()
	retry()
	if e = a.RunBackupOnce(ctx); e == nil {
		t.Fatal("remote failure was not reported")
	}
	failing.Store(false)
	retry()
	if e = a.RunBackupOnce(ctx); e != nil {
		t.Fatal("copy retry failed", e)
	}
	var state, remoteKey string
	var attempts int
	if e = p.QueryRow(ctx, `SELECT state,attempts,remote_key FROM backup_copies WHERE task_id=$1`, job).Scan(&state, &attempts, &remoteKey); e != nil || state != "verified" || attempts != 2 {
		t.Fatal("copy ledger mismatch", e, state, attempts)
	}
	actual, e := os.ReadFile(filepath.Join(remoteDir, remoteKey))
	if e != nil || !bytes.Equal(actual, payload) {
		t.Fatal("remote payload mismatch", e)
	}
	// Restore quarantine must never delete the original deployment's copies.
	if e = s.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.PruneRemote(ctx, a.config.VaultKey); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(remoteDir, remoteKey)); e != nil {
		t.Fatal("quarantine deleted source copy", e)
	}
	if _, e = p.Exec(ctx, `UPDATE backup_copies SET state='verified',reason='' WHERE task_id=$1`, job); e != nil {
		t.Fatal(e)
	}
	if e = s.PruneRemote(ctx, a.config.VaultKey); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(remoteDir, remoteKey)); !os.IsNotExist(e) {
		t.Fatal("expired remote copy retained", e)
	}
	response := call(t, srv.URL, "GET", "/api/admin/backup/copies", nil, admin, origin)
	expect(t, response, 200)
	var rows []map[string]any
	if e = json.Unmarshal(response.body, &rows); e != nil || len(rows) != 1 || rows[0]["state"] != "deleted" {
		t.Fatal("copy cleanup not visible", e)
	}
}
