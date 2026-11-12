package application

import "testing"

func TestR16RouteCatalogUsesRealRegistration(t *testing.T) {
	routes := RegisteredHTTPRoutes()
	seen := map[string]bool{}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if seen[key] {
			t.Fatal("duplicate route", key)
		}
		seen[key] = true
		if route.Fallback && key != "* /api/" {
			t.Fatal("unexpected catch-all", key)
		}
	}
	for _, key := range []string{"GET /healthz", "PATCH /api/settings", "GET /api/admin/agent-installations/config", "GET /api/sessions/{id}/ws", "POST /api/admin/ai/profiles", "* /api/"} {
		if !seen[key] {
			t.Fatal("route missing", key)
		}
	}
	if len(routes) < 300 {
		t.Fatal("incomplete route registration", len(routes))
	}
}
