package application

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/system"
	"testing"
)

func TestR16IdleActivityLeaseAndBarrier(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	apply("idle-a")
	apply("idle-b")
	patch := func(value any, status int) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"access-max-idle-second": value}}, admin)
		expect(t, r, status)
	}
	for _, invalid := range []any{0, -2, 86401, 1.5, true} {
		patch(invalid, 422)
	}
	patch(10, 202)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+id)
	age := func(seconds int) {
		t.Helper()
		if _, e := pool.Exec(ctx, `UPDATE sessions SET last_activity_at=clock_timestamp()-make_interval(secs=>$2) WHERE id=$1`, id, seconds); e != nil {
			t.Fatal(e)
		}
	}
	age(5)
	if e := ws.WriteMessage(websocket.TextMessage, []byte("1hello\n")); e != nil {
		t.Fatal(e)
	}
	readUntil(t, ws, "hello")
	var recent bool
	if e := pool.QueryRow(ctx, `SELECT last_activity_at>clock_timestamp()-interval '2 seconds' FROM sessions WHERE id=$1`, id).Scan(&recent); e != nil || !recent {
		t.Fatal("input did not refresh activity", e)
	}
	age(5)
	if e := ws.WriteMessage(websocket.TextMessage, []byte("9keepalive")); e != nil {
		t.Fatal(e)
	}
	readUntil(t, ws, "9keepalive")
	if e := pool.QueryRow(ctx, `SELECT last_activity_at<clock_timestamp()-interval '4 seconds' FROM sessions WHERE id=$1`, id).Scan(&recent); e != nil || !recent {
		t.Fatal("heartbeat refreshed activity", e)
	}
	// Relaxation is not effective until BOTH registered consumers acknowledge.
	patch(-1, 202)
	effective, e := a.system.SecurityConfiguration(ctx)
	if e != nil || effective.Values.Int("access-max-idle-second") != 10 {
		t.Fatal("relaxed before ACK", e)
	}
	apply("idle-a")
	effective, e = a.system.SecurityConfiguration(ctx)
	if e != nil || effective.Values.Int("access-max-idle-second") != 10 {
		t.Fatal("partial ACK relaxed", e)
	}
	apply("idle-b")
	age(3600)
	if e = a.sessions.Renew(ctx, id); e != nil {
		t.Fatal("disabled idle still timed out", e)
	}
	// Tightening applies immediately, and a late input cannot resurrect the session.
	patch(10, 202)
	if e = a.sessions.Activity(ctx, id); !errors.Is(e, session.ErrIdle) {
		t.Fatal("late activity accepted", e)
	}
	if e = a.sessions.Renew(ctx, id); !errors.Is(e, session.ErrIdle) {
		t.Fatal("idle lease extended", e)
	}
	if got := waitClosed(t, a, id); got != "idle_timeout" {
		t.Fatal("idle close state", got)
	}
	v, e := a.sessions.Get(ctx, id)
	if e != nil || v.Reason != "idle_timeout" {
		t.Fatal("idle reason", v.Reason, e)
	}
	if e = a.sessions.Activity(ctx, id); e == nil {
		t.Fatal("closed session revived")
	}
}

func TestR16ReadonlyHeartbeatExpires(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, ticket := reserve(t, srv, admin, asset, true)
	ws := attach(t, srv, admin, id, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+id)
	if _, e := pool.Exec(ctx, `UPDATE sessions SET last_activity_at=clock_timestamp()-interval '20 minutes' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1denied\r"))
	_ = ws.WriteMessage(websocket.TextMessage, []byte("9heartbeat"))
	waitClosed(t, a, id)
	v, e := a.sessions.Get(ctx, id)
	if e != nil || v.Reason != "idle_timeout" {
		t.Fatal("readonly/heartbeat extended session", v.Reason, e)
	}
}

func TestR16IdleDoesNotReviveExpiredTransport(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, ticket := reserve(t, srv, admin, asset, false)
	ws := attach(t, srv, admin, id, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+id)
	if _, e := pool.Exec(ctx, `UPDATE sessions SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e := a.sessions.Activity(ctx, id); e == nil {
		t.Fatal("expired transport revived")
	}
}
