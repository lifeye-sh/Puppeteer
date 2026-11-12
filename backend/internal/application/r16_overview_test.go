package application

import (
	"context"
	"testing"
)

func TestR16PlatformOverview(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("metrics-member", nil, nil), admin)
	member := loginAs(t, srv, "metrics-member")
	expect(t, call(t, srv.URL, "GET", "/api/admin/monitoring/overview", nil, member, origin), 403)
	v := object(t, call(t, srv.URL, "GET", "/api/admin/monitoring/overview", nil, admin, origin))
	b, ok := v["business"].(map[string]any)
	if !ok || b["databaseBytes"].(float64) <= 0 || b["sessions"].(float64) != 0 {
		t.Fatal(v)
	}
	p := v["pool"].(map[string]any)
	if p["max"].(float64) < 1 || p["acquired"].(float64) > p["total"].(float64) {
		t.Fatal(p)
	}
	if v["node"] != a.configurationNode || v["goroutines"].(float64) < 1 || len(v["errors"].([]any)) != 0 {
		t.Fatal(v)
	}
	// Failed collection is unknown, never a successful zero; process data survives.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	partial := a.operations().Overview(ctx, a.configurationNode)
	if partial.Business != nil || len(partial.Errors) == 0 || partial.Pool.Max < 1 {
		t.Fatal(partial)
	}
}
