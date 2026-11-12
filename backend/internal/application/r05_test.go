package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/files"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func taskInput(id, path string, size int, expected string) map[string]any {
	return map[string]any{"id": id, "path": path, "size": size, "expectedHash": expected}
}
func taskUUID(n int) string { return fmt.Sprintf("90500000-0000-4000-8000-%012d", n) }
func putChunk(t *testing.T, srv *httptest.Server, c *http.Cookie, id string, offset int, data []byte) result {
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/api/uploads/%s/chunks?offset=%d", srv.URL, id, offset), bytes.NewReader(data))
	req.AddCookie(c)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return result{status: res.StatusCode, body: b}
}
func TestR05SFTPLifecycleBoundariesAndAudit(t *testing.T) {
	root := t.TempDir()
	remotePath := func(name string) string { return root + "/" + name }
	outside := t.TempDir()
	if e := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, root)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	base := "/api/sessions/" + id
	r := call(t, srv.URL, "GET", base+"/files", nil, admin, origin)
	expect(t, r, 200)
	if object(t, r)["root"] != "/" {
		t.Fatalf("wrong root %s", r.body)
	}
	// The fixture login home is root; another temporary directory is outside it.
	r = call(t, srv.URL, "GET", base+"/files/content?path="+url.QueryEscape(outside+"/secret"), nil, admin, origin)
	expect(t, r, 200)
	if string(r.body) != "outside" {
		t.Fatal("root navigation remained confined to home")
	}
	r = call(t, srv.URL, "GET", base+"/files?path="+url.QueryEscape(root), nil, admin, origin)
	expect(t, r, 200)
	for _, bad := range []string{"../secret", "escape/secret", "a/../../secret", ".nt-upload-forged"} {
		r = call(t, srv.URL, "GET", base+"/files/content?path="+url.QueryEscape(remotePath(bad)), nil, admin, origin)
		if r.status != 403 && r.status != 422 {
			t.Fatalf("boundary %s: %d %s", bad, r.status, r.body)
		}
	}
	expect(t, call(t, srv.URL, "POST", base+"/files/actions", map[string]string{"action": "mkdir", "path": remotePath("验收")}, admin, origin), 200)
	data := []byte("中文文件内容\nhello binary \x00\xff")
	task := taskUUID(1)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(task, remotePath("验收/测试.bin"), len(data), ""), admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(task, remotePath("验收/测试.bin"), len(data), ""), admin, origin), 200)
	expect(t, putChunk(t, srv, admin, task, 1, data), 409)
	expect(t, putChunk(t, srv, admin, task, 0, data), 200)
	expect(t, putChunk(t, srv, admin, task, 0, data), 200)
	expect(t, putChunk(t, srv, admin, task, 0, []byte("bad")), 409)
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+task+"/commit", nil, admin, origin), 200)
	actual, e := os.ReadFile(filepath.Join(root, "验收", "测试.bin"))
	if e != nil || !bytes.Equal(data, actual) {
		t.Fatal("target content mismatch", e)
	}
	r = call(t, srv.URL, "GET", base+"/files/content?path="+url.QueryEscape(remotePath("验收/测试.bin")), nil, admin, origin)
	expect(t, r, 200)
	if !bytes.Equal(data, r.body) {
		t.Fatal("download changed bytes")
	}
	expect(t, call(t, srv.URL, "GET", base+"/files/content?text=true&path="+url.QueryEscape(remotePath("验收/测试.bin")), nil, admin, origin), 422)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(taskUUID(2), remotePath("验收/测试.bin"), 1, ""), admin, origin), 409)
	// Text editing uses an exact content hash and rejects a stale version at commit.
	if e = os.WriteFile(filepath.Join(root, "edit.txt"), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	r = call(t, srv.URL, "GET", base+"/files/content?text=true&path="+url.QueryEscape(remotePath("edit.txt")), nil, admin, origin)
	expect(t, r, 200)
	hash := object(t, r)["sha256"].(string)
	task = taskUUID(3)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(task, remotePath("edit.txt"), 3, hash), admin, origin), 200)
	expect(t, putChunk(t, srv, admin, task, 0, []byte("new")), 200)
	if e = os.WriteFile(filepath.Join(root, "edit.txt"), []byte("other"), 0600); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+task+"/commit", nil, admin, origin), 409)
	r = call(t, srv.URL, "GET", "/api/uploads/"+task, nil, admin, origin)
	expect(t, r, 200)
	if object(t, r)["state"] != "failed" {
		t.Fatalf("wrong failed task %s", r.body)
	}
	task = taskUUID(4)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(task, remotePath("edit.txt"), 3, files.Hash([]byte("other"))), admin, origin), 200)
	expect(t, putChunk(t, srv, admin, task, 0, []byte("new")), 200)
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+task+"/commit", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", base+"/files/actions", map[string]string{"action": "rename", "path": remotePath("edit.txt"), "destination": remotePath("renamed.txt")}, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", base+"/files/actions", map[string]string{"action": "delete", "path": remotePath("renamed.txt")}, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", base+"/files/actions", map[string]string{"action": "delete", "path": remotePath("验收")}, admin, origin), 422)
	// Replaying commit is idempotent and never recreates a subsequently deleted file.
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+task+"/commit", nil, admin, origin), 200)
	if _, e = os.Stat(filepath.Join(root, "edit.txt")); !os.IsNotExist(e) {
		t.Fatal("commit replay recreated file")
	}
	postID(t, srv, "/api/admin/users", userInput("files-denied", nil, nil), admin)
	denied := loginAs(t, srv, "files-denied")
	expect(t, call(t, srv.URL, "GET", base+"/files", nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/uploads/"+task, nil, denied, origin), 404)
	expect(t, call(t, srv.URL, "GET", "/api/filesystem-logs?all=true", nil, denied, origin), 403)
	r = call(t, srv.URL, "GET", "/api/filesystem-logs?all=true", nil, admin, origin)
	expect(t, r, 200)
	if object(t, r)["total"].(float64) < 8 {
		t.Fatal("missing file audit")
	}
	var chunks int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM upload_chunks`).Scan(&chunks); e != nil || chunks != 0 {
		t.Fatal("staging not reclaimed", chunks, e)
	}
	// Read-only sessions can list/download but cannot start uploads or mutate targets.
	rid, rticket := reserve(t, srv, admin, asset, true)
	rws := attach(t, srv, admin, rid, rticket)
	readUntil(t, rws, "3"+rid)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+rid+"/files", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+rid+"/uploads", taskInput(taskUUID(5), remotePath("nope"), 1, ""), admin, origin), 403)
	_ = rws.Close()
	_ = ws.Close()
	waitClosed(t, a, id)
	expect(t, call(t, srv.URL, "GET", base+"/files", nil, admin, origin), 403)
}
func TestR05QuotaCancelExpiryAndRecovery(t *testing.T) {
	root := t.TempDir()
	remotePath := func(name string) string { return root + "/" + name }
	host, port, _ := sshTargetRoot(t, root)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	base := "/api/sessions/" + id
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(taskUUID(20), remotePath("oversize"), files.MaxFile+1, ""), admin, origin), 422)
	for i := 21; i < 25; i++ {
		expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(taskUUID(i), remotePath(fmt.Sprint("f", i)), files.MaxFile, ""), admin, origin), 200)
	}
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(taskUUID(25), remotePath("f25"), 1, ""), admin, origin), 409)
	expect(t, putChunk(t, srv, admin, taskUUID(21), 0, []byte("partial")), 200)
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+taskUUID(21)+"/cancel", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(taskUUID(25), remotePath("f25"), 1, ""), admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+taskUUID(21)+"/commit", nil, admin, origin), 409)
	if _, e := p.Exec(context.Background(), `UPDATE upload_tasks SET expires_at=now()-interval '1 minute' WHERE state='receiving'`); e != nil {
		t.Fatal(e)
	}
	if e := a.files.Recover(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM upload_chunks`).Scan(&n)
	if n != 0 {
		t.Fatal("cancel/expiry retained chunks")
	}
	// A crash after a temp upload retains its cleanup evidence and releases staging.
	task := taskUUID(26)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(task, remotePath("recovery.txt"), 1, ""), admin, origin), 200)
	expect(t, putChunk(t, srv, admin, task, 0, []byte("x")), 200)
	if e := a.files.Claim(context.Background(), task, object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)); e != nil {
		t.Fatal(e)
	}
	_ = a.files.SetCleanup(context.Background(), task, true)
	tmp := filepath.Join(root, ".nt-upload-"+task)
	if e := os.WriteFile(tmp, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := a.RecoverFiles(context.Background()); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", base+"/files", nil, admin, origin), 200)
	if _, e := os.Stat(tmp); !os.IsNotExist(e) {
		t.Fatal("orphan not reclaimed")
	}
	r := call(t, srv.URL, "GET", "/api/uploads/"+task, nil, admin, origin)
	var result files.Task
	_ = json.Unmarshal(r.body, &result)
	if result.State != "interrupted" || result.Cleanup {
		t.Fatalf("recovery %+v", result)
	}
	// Cancellation just before commit cannot advance to committing.
	task = taskUUID(27)
	expect(t, call(t, srv.URL, "POST", base+"/uploads", taskInput(task, remotePath("canceled"), 0, ""), admin, origin), 200)
	owner := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	if e := a.files.Claim(context.Background(), task, owner); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/uploads/"+task+"/cancel", nil, admin, origin), 200)
	if e := a.files.Committing(context.Background(), task); e == nil {
		t.Fatal("cancel raced into commit")
	}
	if strings.Contains(string(r.body), "private-key") {
		t.Fatal("secret leak")
	}
	_ = ws.WriteMessage(websocket.TextMessage, []byte("9ping"))
}
