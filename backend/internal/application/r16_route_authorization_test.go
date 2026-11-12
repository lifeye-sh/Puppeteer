package application

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Route-wide denial smoke complements the domain tests with valid payloads.
// 400/404/422 may precede authentication and do not prove operation permission.
// Do not label this as the full per-operation positive/negative permission matrix.
func TestR16ManagementRouteAuthorizationMatrix(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("route-denied-member", nil, nil), admin)
	member := loginAs(t, srv, "route-denied-member")
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	for _, route := range RegisteredHTTPRoutes() {
		if route.Fallback || !strings.HasPrefix(route.Path, "/api/admin/") {
			continue
		}
		path := parameter.ReplaceAllString(route.Path, "00000000-0000-4000-8000-000000000001")
		for _, principal := range []struct {
			name   string
			cookie *http.Cookie
		}{{"anonymous", nil}, {"unprivileged", member}} {
			t.Run(principal.name+"/"+route.Method+route.Path, func(t *testing.T) {
				// Give each independent permission check a fresh in-memory rate
				// window; dedicated limiter tests cover exhaustion separately.
				a.mu.Lock()
				a.attempts = map[string]attempt{}
				a.mu.Unlock()
				r := call(t, srv.URL, route.Method, path, map[string]any{}, principal.cookie, origin)
				switch r.status {
				case 400, 401, 403, 404, 405, 422:
				default:
					t.Fatalf("management route unexpectedly admitted or failed: %d", r.status)
				}
			})
		}
		if route.Method != "GET" && route.Method != "HEAD" {
			t.Run("foreign-origin/"+route.Method+route.Path, func(t *testing.T) {
				expect(t, call(t, srv.URL, route.Method, path, map[string]any{}, admin, "https://foreign.invalid"), 403)
			})
		}
	}
}

func TestR16AISettingsReadProjection(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("ai-public-status", nil, nil), admin)
	member := loginAs(t, srv, "ai-public-status")
	expect(t, call(t, srv.URL, "GET", "/api/admin/ai/settings", nil, member, origin), 403)
	management := object(t, call(t, srv.URL, "GET", "/api/admin/ai/settings", nil, admin, origin))
	if _, ok := management["shellPrompt"]; !ok {
		t.Fatal("admin policy not available")
	}
	for _, cookie := range []*http.Cookie{admin, member} {
		status := object(t, call(t, srv.URL, "GET", "/api/ai/status", nil, cookie, origin))
		if len(status) != 5 {
			t.Fatal("public status contains management fields", status)
		}
		for _, key := range []string{"enabled", "readonlyEnabled", "shellEnabled", "auditEnabled", "version"} {
			if _, ok := status[key]; !ok {
				t.Fatal("public status field missing", key)
			}
		}
	}
}
