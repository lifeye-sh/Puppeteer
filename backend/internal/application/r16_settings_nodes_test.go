package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestR16ConfigurationNodeRecoveryMatrix(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	initial, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	apply := func(node string) {
		t.Helper()
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	node := func(name string) system.ConfigNode {
		t.Helper()
		v, e := a.system.ConfigurationView(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		for _, n := range v.Nodes {
			if n.ID == name {
				return n
			}
		}
		t.Fatalf("missing node %s", name)
		return system.ConfigNode{}
	}
	for _, n := range []string{"matrix-a", "matrix-b", "matrix-c"} {
		apply(n)
	}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(initial.ID), map[string]any{"set": map[string]any{"postgres-result-max-rows": 9}}, admin)
	expect(t, r, 202)
	current, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	apply("matrix-a")
	bad := func(system.Revision) error { return errors.New("secret-test-endpoint-password") }
	if _, e = a.system.ApplyConfiguration(ctx, "matrix-b", bad); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE configuration_nodes SET last_seen=now()-interval '1 minute' WHERE id='matrix-c'`); e != nil {
		t.Fatal(e)
	}
	v, e := a.system.ConfigurationView(ctx, 0)
	if e != nil || v.State != "failed" {
		t.Fatal(v, e)
	}
	if n := node("matrix-b"); n.LastAppliedRevision != initial.ID || n.Attempts != 1 || strings.Contains(n.Error, "secret-test") {
		t.Fatal(n)
	}
	if n := node("matrix-c"); n.Online || n.LastAppliedRevision != initial.ID || n.State != "requested" {
		t.Fatal(n)
	}
	called := false
	if _, e = a.system.ApplyConfiguration(ctx, "matrix-b", func(system.Revision) error { called = true; return nil }); !errors.Is(e, system.ErrApplyFailed) || called {
		t.Fatal("failure latch bypassed", e, called)
	}
	// Only the current revision may be retried; healthy ACKs remain untouched.
	if e = a.db.Within(ctx, func(tx context.Context) error { return a.system.RetryConfiguration(tx, initial.ID) }); !errors.Is(e, fault.Conflict) {
		t.Fatal(e)
	}
	if e = a.db.Within(ctx, func(tx context.Context) error { return a.system.RetryConfiguration(tx, current.ID) }); e != nil {
		t.Fatal(e)
	}
	if n := node("matrix-a"); n.Attempts != 1 || n.State != "effective" {
		t.Fatal(n)
	}
	apply("matrix-b")
	if n := node("matrix-b"); n.LastAppliedRevision != current.ID || n.Attempts != 2 || n.Error != "" {
		t.Fatal(n)
	}
	v, e = a.system.ConfigurationView(ctx, 0)
	if e != nil || v.State != "effective" {
		t.Fatal("online nodes not converged", v, e)
	}
	// Offline stale nodes cannot reuse their old snapshot when returning.
	returning, e := a.system.ApplyConfiguration(ctx, "matrix-c", func(r system.Revision) error {
		if r.ID != current.ID || r.Values.Int("postgres-result-max-rows") != 9 {
			return errors.New("stale snapshot")
		}
		return nil
	})
	if e != nil || returning.ID != current.ID {
		t.Fatal(returning, e)
	}
	apply("matrix-new")
	if n := node("matrix-new"); n.LastAppliedRevision != current.ID || n.Attempts != 1 {
		t.Fatal(n)
	}
	// An effective node can fail a later recheck; count this as a new round,
	// keep it closed until explicit retry, and don't count ordinary heartbeats.
	apply("matrix-a")
	if _, e = a.system.ApplyConfiguration(ctx, "matrix-a", bad); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal(e)
	}
	if n := node("matrix-a"); n.Attempts != 2 {
		t.Fatal("failed recheck was not counted", n)
	}
	if e = a.db.Within(ctx, func(tx context.Context) error { return a.system.RetryConfiguration(tx, current.ID) }); e != nil {
		t.Fatal(e)
	}
	apply("matrix-a")
	if n := node("matrix-a"); n.Attempts != 3 {
		t.Fatal(n)
	}
	history, e := a.system.ConfigurationView(ctx, initial.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range history.Nodes {
		if n.ID == "matrix-c" && (n.State != "effective" || n.LastAppliedRevision != current.ID) {
			t.Fatal("history ACK changed or latest confirmation hidden", n)
		}
	}
	// Real authenticated endpoint exposes public diagnostics, not callback errors.
	result := call(t, srv.URL, "GET", "/api/settings", nil, admin, origin)
	expect(t, result, 200)
	if !strings.Contains(string(result.body), `"lastAppliedRevision":`) || strings.Contains(string(result.body), "secret-test") {
		t.Fatal("invalid node diagnostics")
	}
	if _, e = pool.Exec(ctx, `UPDATE configuration_nodes SET last_seen=now()-interval '1 minute'`); e != nil {
		t.Fatal(e)
	}
	v, e = a.system.ConfigurationView(ctx, 0)
	if e != nil || v.State == "effective" {
		t.Fatal("all offline reported effective", v, e)
	}
}

func TestR16ConfigurationApplyPublishSerialization(t *testing.T) {
	_, a, _ := r02Server(t, false)
	ctx := context.Background()
	var actor string
	if e := a.db.Row(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	initial, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		_, e := a.system.ApplyConfiguration(ctx, "slow-node", func(r system.Revision) error {
			close(entered)
			<-release
			if r.ID != initial.ID {
				return errors.New("unexpected revision")
			}
			return nil
		})
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare did not start")
	}
	write := func(ctx context.Context, expected int64) (system.Revision, error) {
		var result system.Revision
		e := a.db.Within(ctx, func(tx context.Context) error {
			var e error
			result, e = a.system.SaveConfiguration(tx, &expected, map[string]json.RawMessage{"postgres-result-max-rows": json.RawMessage("7")}, actor)
			return e
		})
		return result, e
	}
	// A timed-out writer must not advance the head while old prepare/ACK is active.
	deadline, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	_, e = write(deadline, initial.ID)
	cancel()
	if e == nil {
		t.Fatal("writer passed active prepare")
	}
	unchanged, e := a.system.Configuration(ctx, 0)
	if e != nil || unchanged.ID != initial.ID {
		t.Fatal("timed-out write committed", e)
	}
	unblock()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("apply did not finish")
	}
	next, e := write(ctx, initial.ID)
	if e != nil {
		t.Fatal(e)
	}
	applied, e := a.system.ApplyConfiguration(ctx, "slow-node", func(r system.Revision) error {
		if r.ID != next.ID || r.Values.Int("postgres-result-max-rows") != 7 {
			return errors.New("out-of-order snapshot")
		}
		return nil
	})
	if e != nil || applied.ID != next.ID {
		t.Fatal("did not apply current head", e)
	}
	// Concurrent consumers with one node identity are serialized by the DB row.
	var active, peak atomic.Int32
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, e := a.system.ApplyConfiguration(ctx, "same-node", func(system.Revision) error {
				n := active.Add(1)
				if n > peak.Load() {
					peak.Store(n)
				}
				time.Sleep(5 * time.Millisecond)
				active.Add(-1)
				return nil
			})
			results <- e
		}()
	}
	for i := 0; i < 8; i++ {
		if e = <-results; e != nil {
			t.Fatal(e)
		}
	}
	if peak.Load() != 1 {
		t.Fatal("concurrent prepare for same node", peak.Load())
	}
	// Cancellation cannot commit a success ACK, nor return success for new work.
	cancelled, stop := context.WithCancel(ctx)
	_, e = a.system.ApplyConfiguration(cancelled, "cancelled-node", func(system.Revision) error { stop(); return nil })
	if e == nil {
		t.Fatal("cancelled prepare accepted")
	}
	view, e := a.system.ConfigurationView(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range view.Nodes {
		if n.ID == "cancelled-node" {
			t.Fatal("cancelled transaction leaked node ACK", n)
		}
	}
}
