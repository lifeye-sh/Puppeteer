package application

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"next-terminal/backend/internal/modules/audit"
	"strings"
	"testing"
	"time"
)

func TestTerminalCommandsAuditPrivacyPaginationAndFailure(t *testing.T) {
	host, port, _ := sshTarget(t)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	var command audit.Command
	var page struct {
		Items []audit.Command `json:"items"`
		Total int             `json:"total"`
	}
	r := call(t, srv.URL, "GET", "/api/sessions/"+id+"/commands?current=1&pageSize=20", nil, admin, origin)
	expect(t, r, 200)
	if e := json.Unmarshal(r.body, &page); e != nil {
		t.Fatal(e)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("missing terminal audit: %s", r.body)
	}
	command = page.Items[0]
	if command.Command != "whoami" || command.Source != "terminal-input" || command.State != "observed" || command.ExitCode != nil {
		t.Fatalf("wrong audit: %+v", command)
	}
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1hidden-secret\n"))
	readUntil(t, ws, "fixture$ ")
	// The submission exists, but no raw password appears in command audit or recording.
	var n int
	e := p.QueryRow(context.Background(), `SELECT count(*) FROM session_commands WHERE session_id=$1 AND source='terminal-input' AND state='unrecognized' AND command=''`, id).Scan(&n)
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	var leak bool
	_ = p.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM session_commands WHERE session_id=$1 AND command LIKE '%hidden-secret%') OR EXISTS(SELECT 1 FROM recording_chunks WHERE session_id=$1 AND convert_from(payload,'UTF8') LIKE '%hidden-secret%')`, id).Scan(&leak)
	if leak {
		t.Fatal("no-echo input leaked")
	}
	// More than the old 100-row cap: every historical entry remains addressable.
	_, e = p.Exec(context.Background(), `INSERT INTO session_commands(session_id,actor_id,command,source,state,created_at) SELECT $1,actor_id,'history-'||g,'explicit-exec','succeeded',now()-g*interval '1 second' FROM sessions CROSS JOIN generate_series(1,105) g WHERE id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	r = call(t, srv.URL, "GET", "/api/sessions/"+id+"/commands?current=6&pageSize=20", nil, admin, origin)
	expect(t, r, 200)
	if e = json.Unmarshal(r.body, &page); e != nil || page.Total != 107 || len(page.Items) != 7 {
		t.Fatalf("pagination %s %v", r.body, e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/commands?current=0", nil, admin, origin), 422)
	postID(t, srv, "/api/admin/users", userInput("audit-denied", nil, nil), admin)
	denied := loginAs(t, srv, "audit-denied")
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/commands?current=1", nil, denied, origin), 403)
	_, _ = p.Exec(context.Background(), `UPDATE recordings SET command_count=10000 WHERE session_id=$1`, id)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1must-not-execute\n"))
	if got := waitClosed(t, a, id); got != "command_audit_failed" {
		t.Fatal(got)
	}
	var data string
	_ = p.QueryRow(context.Background(), `SELECT COALESCE(string_agg(convert_from(payload,'UTF8'),''),'') FROM recording_chunks WHERE session_id=$1`, id).Scan(&data)
	if strings.Contains(data, "must-not-execute") {
		t.Fatal("input forwarded without audit")
	}
	// Fresh read-only session rejects terminal input without writing command records.
	id, ticket = reserve(t, srv, admin, asset, true)
	ws = attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "只读会话禁止输入")
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM session_commands WHERE session_id=$1`, id).Scan(&n)
	if n != 0 {
		t.Fatal("read-only input audited as submitted")
	}
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
}
