package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"testing"
)

func TestR16RecordingPolicyKeepsCommandsAndSessionSnapshot(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	old, _ := reserve(t, srv, admin, asset, false)
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	save := func(enabled bool, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": map[string]any{"recording-enabled": enabled}, "password": password}, admin)
		expect(t, r, want)
	}
	apply("record-a")
	apply("record-b")
	save(false, "", 422)
	save(false, testPassword, 202)
	enabled := func(id string) bool {
		t.Helper()
		v, e := a.audit.RecordingEnabled(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	pending, _ := reserve(t, srv, admin, asset, false)
	if !enabled(pending) {
		t.Fatal("disabled before ACK")
	}
	a.finishSession(pending, "closed", "fixture")
	apply("record-a")
	apply("record-b")
	id, ticket := reserve(t, srv, admin, asset, false)
	if enabled(id) || !enabled(old) {
		t.Fatal("session snapshot not preserved")
	}
	ws := attach(t, srv, admin, id, ticket)
	readUntil(t, ws, "3"+id)
	if e := ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n")); e != nil {
		t.Fatal(e)
	}
	readUntil(t, ws, "EXECUTED whoami")
	r := call(t, srv.URL, "GET", "/api/sessions/"+id+"/commands?current=1&pageSize=20", nil, admin, origin)
	expect(t, r, 200)
	if !strings.Contains(string(r.body), `"command":"whoami"`) {
		t.Fatal("disabled recording disabled command audit", string(r.body))
	}
	var chunks int
	var bytes int64
	if e := pool.QueryRow(ctx, `SELECT bytes,(SELECT count(*) FROM recording_chunks WHERE session_id=$1) FROM recordings WHERE session_id=$1`, id).Scan(&bytes, &chunks); e != nil || bytes != 0 || chunks != 0 {
		t.Fatal(bytes, chunks, e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+id+"/shares", map[string]any{"username": "accept-admin", "minutes": 1}, admin, origin), 409)
	if _, e := a.audit.Observe(ctx, id, 0); !errors.Is(e, fault.Denied) {
		t.Fatal("unrecorded observer", e)
	}
	save(true, testPassword, 202)
	next, _ := reserve(t, srv, admin, asset, false)
	if !enabled(next) || enabled(id) {
		t.Fatal("reenable changed existing session")
	}
	if e := a.audit.Record(ctx, old, "output", 0, []byte("preserved recording")); e != nil {
		t.Fatal(e)
	}
	if e := pool.QueryRow(ctx, `SELECT bytes FROM recordings WHERE session_id=$1`, old).Scan(&bytes); e != nil || bytes == 0 {
		t.Fatal("old recording lost", e)
	}
	// Command quota still stops an unrecorded live connection before input reaches target.
	if _, e := pool.Exec(ctx, `UPDATE recordings SET command_count=10000 WHERE session_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	ws.WriteMessage(websocket.TextMessage, []byte("1must-not-run\n"))
	if reason := waitClosed(t, a, id); reason != "command_audit_failed" {
		t.Fatal(reason)
	}
	access, e := a.audit.Access(ctx, id)
	if e != nil || access.RecordingState != "disabled" {
		t.Fatal(access, e)
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"recording-enabled": json.RawMessage("false")}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("service bypass", e)
	}
	save(false, testPassword, 202)
	apply("record-a")
	apply("record-b")
	if e = a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if !head().Values.Bool("recording-enabled") {
		t.Fatal("restore disabled future audit")
	}
}
func TestR16RecordingConfigurationFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"recording-enabled": false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
