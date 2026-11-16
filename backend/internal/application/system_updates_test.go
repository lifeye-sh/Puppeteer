package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"puppeteer/backend/internal/modules/notification"
	"puppeteer/backend/internal/platform/systemupdate"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSystemUpdatesAuthorizationAndCompatibility(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("update-member", nil, nil), admin)
	member := loginAs(t, srv, "update-member")
	expect(t, call(t, srv.URL, "GET", "/api/admin/updates", nil, nil, origin), 401)
	for _, p := range []string{"/api/admin/updates", "/api/admin/updates/inventory", "/api/admin/updates/task", "/api/admin/updates/automation", "/api/admin/updates/history"} {
		expect(t, call(t, srv.URL, "GET", p, nil, member, origin), 403)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/check", map[string]any{}, member, origin), 403)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/settings", map[string]any{}, member, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/preflight", map[string]any{"version": "beta0.11"}, member, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/tasks", map[string]any{}, member, origin), 403)
	now := time.Now().UTC()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	release := systemupdate.Release{Version: "beta0.11", Channel: "beta", PublishedAt: now, Platform: "linux/amd64", MinSource: "beta0.10", SchemaVersion: 110, CapabilitiesRevision: 90, NotesURL: "https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.11"}
	for _, c := range []string{"server", "web", "graphics", "renderer"} {
		d := "sha256:" + strings.Repeat("a", 64)
		release.Images = append(release.Images, systemupdate.Image{Component: c, Digest: d, PinnedReference: "cloners/puppeteer-" + c + "@" + d})
	}
	payload, _ := json.Marshal(systemupdate.Catalog{Product: "Puppeteer", Format: 1, Sequence: 1, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Releases: []systemupdate.Release{release}})
	raw, _ := json.Marshal(systemupdate.Envelope{KeyID: "fixture", Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	m, e := systemupdate.New("", map[string]ed25519.PublicKey{"fixture": pub}, func(context.Context) ([]byte, error) { return raw, nil })
	if e != nil {
		t.Fatal(e)
	}
	a.updateManager = m
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/check", map[string]any{}, admin, origin), 200)
	got := object(t, call(t, srv.URL, "GET", "/api/admin/updates", nil, admin, origin))
	if got["updates"].(map[string]any)["state"] != "update_available" {
		t.Fatal(got)
	}
	preflight := object(t, call(t, srv.URL, "POST", "/api/admin/updates/preflight", map[string]any{"version": "beta0.11"}, admin, origin))
	if preflight["passed"] != false || preflight["automaticInstall"] != false {
		t.Fatal("missing runner passed", preflight)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/preflight", map[string]any{"version": "beta0.12"}, admin, origin), 422)
	// The browser cannot select actor, signed metadata or host commands. Password
	// proof and audit commit must finish before any privileged host dispatch.
	dir := t.TempDir()
	a.config.DeploySocket = filepath.Join(dir, "deploy.sock")
	a.config.DeployTokenFile = filepath.Join(dir, "token")
	if e = os.WriteFile(a.config.DeployTokenFile, []byte(strings.Repeat("c", 64)), 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("unix", a.config.DeploySocket)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	received := make(chan map[string]any, 4)
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("c", 64) {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/updates/task" {
			w.Write([]byte(`{"task":{"id":"fixture","stage":"prepared","progressToken":"do-not-expose-progress-secret"}}`))
			return
		}
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received <- body
		w.WriteHeader(202)
		w.Write([]byte(`{"task":{"id":"accepted","stage":"queued"}}`))
	})}
	go host.Serve(listener)
	t.Cleanup(func() { host.Close() })
	body := map[string]any{"id": strings.Repeat("a", 32), "version": "beta0.11", "layoutDigest": strings.Repeat("b", 64), "generation": 0, "confirmedStop": true, "password": "wrong"}
	bad := call(t, srv.URL, "POST", "/api/admin/updates/tasks", body, admin, origin)
	if bad.status == 202 || calls.Load() != 0 {
		t.Fatal("invalid proof dispatched upgrade")
	}
	body["password"] = testPassword
	body["confirmedStop"] = false
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/tasks", body, admin, origin), 422)
	body["confirmedStop"] = true
	for _, field := range []string{"actor", "envelope", "command", "image"} {
		body[field] = "forged"
		expect(t, call(t, srv.URL, "POST", "/api/admin/updates/tasks", body, admin, origin), 400)
		delete(body, field)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached host")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/tasks", body, admin, origin), 202)
	sent := <-received
	if sent["actor"] == "" || sent["actor"] == nil || sent["password"] != nil || sent["totp"] != nil {
		t.Fatal("missing server actor or proof forwarded")
	}
	gotEnvelope, _ := json.Marshal(sent["envelope"])
	var signed systemupdate.Envelope
	if json.Unmarshal(gotEnvelope, &signed) != nil || signed.KeyID != "fixture" {
		t.Fatal("cached signed target not forwarded")
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/updates/task", nil, admin, origin), 200)
	policy := systemupdate.DefaultAutomation()
	policy.Mode = "window"
	policy.MaxVersion = "beta0.11"
	policy.ExpiresAt = time.Now().Add(time.Hour)
	automation := map[string]any{"policy": policy, "password": testPassword, "confirmedStop": true}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/automation", automation, member, origin), 403)
	automation["confirmedStop"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/automation", automation, admin, origin), 422)
	automation["confirmedStop"] = true
	automation["actor"] = "forged"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/automation", automation, admin, origin), 400)
	delete(automation, "actor")
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/automation", automation, admin, origin), 202)
	sent = <-received
	if sent["actor"] == nil || sent["password"] != nil || sent["totp"] != nil {
		t.Fatal("unsafe automation delegation")
	}
	// The AI entry is read-only and uses the same administrator boundary.
	profile := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "update-assistant", "baseUrl": "https://model.invalid", "model": "fixture", "enabled": true, "maxOutputTokens": 64}, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "readonlyEnabled": true, "version": 1}, admin, origin), 200)
	conversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "system updates"}, admin)
	memberConversation := postID(t, srv, "/api/ai/conversations", map[string]any{"profileId": profile, "title": "system updates"}, member)
	proposal := map[string]any{"clientId": taskUUID(17901), "name": "system_update_status", "arguments": map[string]any{}}
	expect(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+memberConversation+"/tools", proposal, member, origin), 403)
	tool := object(t, call(t, srv.URL, "POST", "/api/ai/conversations/"+conversation+"/tools", proposal, admin, origin))
	expect(t, call(t, srv.URL, "POST", "/api/ai/tools/"+tool["id"].(string)+"/decide", map[string]any{"decision": "approve", "parameterHash": tool["parameterHash"], "revision": tool["revision"]}, admin, origin), 200)
	executed := object(t, call(t, srv.URL, "POST", "/api/ai/tools/"+tool["id"].(string)+"/execute", map[string]any{}, admin, origin))
	if executed["state"] != "succeeded" || !strings.Contains(executed["output"].(string), "currentVersion") || strings.Contains(executed["output"].(string), "progressToken") {
		t.Fatal("AI update inspection failed", executed)
	}
	settings := map[string]any{"revision": 1, "channel": "stable", "autoCheck": false, "intervalHours": 24}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/settings", settings, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/settings", settings, admin, origin), 409)
	expect(t, call(t, srv.URL, "POST", "/api/admin/updates/preflight", map[string]any{"version": "beta0.11"}, admin, origin), 422)
	settings["revision"] = float64(2)
	settings["arbitraryScript"] = "id"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/updates/settings", settings, admin, origin), 400)
}

func TestSystemUpdateNotificationDedupAndRecipientAuthorization(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	member := postID(t, srv, "/api/admin/users", userInput("update-notice-member", nil, nil), admin)
	var actor string
	if e := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	a.config.DeploySocket = filepath.Join(dir, "notice.sock")
	a.config.DeployTokenFile = filepath.Join(dir, "token")
	if e := os.WriteFile(a.config.DeployTokenFile, []byte(strings.Repeat("c", 64)), 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("unix", a.config.DeploySocket)
	if e != nil {
		t.Fatal(e)
	}
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"lastStage": "queued", "notices": []map[string]string{
			{"actor": actor, "task": strings.Repeat("a", 32), "stage": "succeeded", "reason": "fixture completed"},
			{"actor": actor, "task": strings.Repeat("b", 32), "stage": "prepared", "reason": "previous policy result"},
		}})
	})}
	go host.Serve(listener)
	t.Cleanup(func() { host.Close() })
	a.collectUpdateNotifications(context.Background())
	a.collectUpdateNotifications(context.Background())
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM notification_events WHERE event_type='system.upgrade.succeeded'`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM notification_events WHERE event_type='system.upgrade.prepared'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("lost previous policy notice", count, e)
	}
	channel := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "update fixture", Type: "smtp", Enabled: true, Config: notification.Config{Host: "smtp.example.test", Port: 587, From: "noreply@example.test"}}, admin)
	rule := notification.Rule{Name: "updates", Enabled: true, Events: []string{"system.upgrade.succeeded"}, ChannelIDs: []string{channel}, Mode: "requester"}
	expect(t, call(t, srv.URL, "POST", "/api/admin/notifications/rules", rule, admin, origin), 422)
	rule.Mode = "specified"
	rule.RecipientIDs = []string{actor, member}
	postID(t, srv, "/api/admin/notifications/rules", rule, admin)
	if e = a.notifications().Expand(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, user := range []string{actor, member} {
		var id string
		if e = pool.QueryRow(context.Background(), `SELECT id FROM notification_deliveries WHERE recipient_id=$1`, user).Scan(&id); e != nil {
			t.Fatal(e)
		}
		ok, e := a.notificationEligible(context.Background(), id, user)
		if e != nil || ok != (user == actor) {
			t.Fatal("unsafe notification recipient", user, ok, e)
		}
	}
}
