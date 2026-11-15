package application

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"puppeteer/backend/internal/modules/website"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentAuthorizationAndMissingEnrollment(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("deployment-member", nil, nil), admin)
	member := loginAs(t, srv, "deployment-member")
	expect(t, call(t, srv.URL, "GET", "/api/admin/deployment/status", nil, member, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/deployment/status", nil, admin, origin), 503)
	expect(t, call(t, srv.URL, "POST", "/api/admin/deployment/preflight", map[string]any{"request": map[string]any{"protocol": "shell"}}, admin, origin), 422)
	expect(t, call(t, srv.URL, "POST", "/api/admin/deployment/tasks", map[string]any{"request": map[string]any{"protocol": "mysql"}}, member, origin), 403)
}

func TestManagedListenerRejectsLegacyToggleChanges(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	a.config.ManagedDeploymentProtocols = map[string]bool{"mysql": true}
	current, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	expect(t, current, 200)
	changed, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"db-proxy-enabled": false}}, admin)
	expect(t, changed, 409)
}

func TestEnrolledListenersCannotBypassDeploymentBeforeFirstTask(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	a.config.DeploySocket = "/unavailable/deployd.sock"
	current, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	expect(t, current, 200)
	for _, key := range []string{"db-proxy-addr", "postgres-proxy-addr", "ssh-server-addr", "website-server-addr", "http-server-addr"} {
		changed, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{key: "0.0.0.0:32123"}}, admin)
		expect(t, changed, 409)
	}
}

func TestManagedWebsiteMutationRequiresIdleDeploymentAndDisabledDeletion(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cert := r09Certificate(t, a, "managed.example.test")
	id := postID(t, srv, "/api/admin/websites", website.Website{Name: "managed", Enabled: true, Domain: "managed.example.test", EntryPath: "/", Upstream: "http://127.0.0.1:8080", Mode: "public", CertificateID: cert, TempTTL: 30, TempMax: 120}, admin)
	site, err := a.websites().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "deploy.sock")
	token := filepath.Join(dir, "token")
	if err = os.WriteFile(token, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"task":null}`)) })}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	a.config.DeploySocket = socket
	a.config.DeployTokenFile = token
	expect(t, call(t, srv.URL, "DELETE", fmt.Sprintf("/api/admin/websites/%s?version=%d", id, site.Version), nil, admin, origin), 409)
	site.Enabled = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/websites/"+id, site, admin, origin), 409)
	server.Close()
	expect(t, call(t, srv.URL, "DELETE", fmt.Sprintf("/api/admin/websites/%s?version=%d", id, site.Version), nil, admin, origin), 503)
}
