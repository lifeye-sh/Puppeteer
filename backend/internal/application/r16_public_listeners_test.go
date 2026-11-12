package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
	"testing"
	"time"
)

func TestR16PublicListenerRollbackRetryAndMaintenance(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := a.StartPublicListeners(ctx, "127.0.0.1:0", ""); e != nil {
		t.Fatal(e)
	}
	defer a.ClosePublicListeners()
	a.listenerManager = listener.New(ctx, 8)
	defer func() { a.listenerManager.Close(); a.listenerManager.Wait() }()
	address := func() string {
		for _, s := range a.publicListeners.States() {
			if s.Name == "http" {
				return s.Address
			}
		}
		return ""
	}
	health := func(want int) {
		t.Helper()
		c := http.Client{Timeout: time.Second}
		r, e := c.Get("http://" + address() + "/healthz")
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		c.CloseIdleConnections()
		if r.StatusCode != want {
			t.Fatal(r.StatusCode)
		}
	}
	old := address()
	health(200)
	change := func(p map[string]any) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": p}, admin)
		expect(t, r, 202)
	}
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	nextListener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	next := nextListener.Addr().String()
	nextListener.Close()
	// A later business bind failure must abort the already prepared HTTP port.
	change(map[string]any{"http-server-addr": next, "ssh-server-addr": occupied.Addr().String()})
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal(e)
	}
	if address() != old {
		t.Fatal("failed revision switched public listener")
	}
	health(200)
	if c, e := net.DialTimeout("tcp", next, time.Second); e == nil {
		c.Close()
		t.Fatal("aborted public preparation leaked")
	}
	occupied.Close()
	v, _ := a.system.Configuration(ctx, 0)
	if _, e = a.appliedConfiguration(ctx); !errors.Is(e, system.ErrApplyFailed) {
		t.Fatal("failed revision not latched", e)
	}
	if e = a.db.Within(ctx, func(c context.Context) error { return a.system.RetryConfiguration(c, v.ID) }); e != nil {
		t.Fatal(e)
	}
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	if address() != next {
		t.Fatal("retry did not switch")
	}
	health(200)
	// Snapshot maintenance drains business workers but retains management health.
	finish, e := a.beginMaintenance(ctx)
	if e != nil {
		t.Fatal(e)
	}
	health(200)
	finish()
	health(200)
}

func TestR16FrozenManagementBootstrap(t *testing.T) {
	_, a, _ := r02Server(t, false)
	a.maintenance.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := a.StartPublicListeners(ctx, "127.0.0.1:0", "127.0.0.1:0"); e != nil {
		t.Fatal(e)
	}
	defer a.ClosePublicListeners()
	states := a.publicListeners.States()
	if len(states) != 1 || states[0].Name != "http" {
		t.Fatal("frozen bootstrap opened business website", states)
	}
	c := http.Client{Timeout: time.Second}
	defer c.CloseIdleConnections()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/readyz", 200}, {"/api/system/runtime", 503}} {
		r, e := c.Get("http://" + states[0].Address + tc.path)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != tc.status {
			t.Fatal(tc.path, r.StatusCode)
		}
	}
}
