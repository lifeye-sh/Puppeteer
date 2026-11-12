package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/storage"
	"syscall"
	"testing"
)

func TestR08ApprovalAtomicGrantAndCancellation(t *testing.T) {
	pool, app, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	requesterID := postID(t, srv, "/api/admin/users", userInput("r08-requester", nil, nil), admin)
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "reviewer", "permissions": []string{"approval.review", "approval.read"}}, admin)
	reviewerID := postID(t, srv, "/api/admin/users", userInput("r08-reviewer", []string{role}, nil), admin)
	requester := loginAs(t, srv, "r08-requester")
	reviewer := loginAs(t, srv, "r08-reviewer")
	credential := postID(t, srv, "/api/admin/credentials", credInput("r08 credential"), admin)
	asset := postID(t, srv, "/api/admin/assets", assetInput("requestable", credential, ""), admin)
	scope := postID(t, srv, "/api/admin/approval-scopes", map[string]any{"name": "test scope", "requesterIds": []string{requesterID}, "reviewerIds": []string{reviewerID}, "resourceIds": []string{asset}, "enabled": true, "maxMinutes": 30}, admin)
	countAssets(t, srv, requester, "/api/portal/assets", 0)
	submitted := call(t, srv.URL, "POST", "/api/access-requests", map[string]any{"scopeId": scope, "resourceId": asset, "reason": "temporary maintenance", "durationMinutes": 10}, requester, origin)
	expect(t, submitted, 201)
	id := object(t, submitted)["id"].(string)
	path := "/api/access-requests/" + id + "/decision"
	decision := map[string]any{"version": 1, "action": "approve"}
	expect(t, call(t, srv.URL, "POST", path, decision, requester, origin), 403)
	expect(t, call(t, srv.URL, "POST", path, decision, reviewer, origin), 200)
	expect(t, call(t, srv.URL, "POST", path, decision, reviewer, origin), 409)
	countAssets(t, srv, requester, "/api/portal/assets", 1)
	var grants, events int
	if e := pool.QueryRow(context.Background(), "SELECT count(*) FROM grants WHERE approval_id=$1", id).Scan(&grants); e != nil {
		t.Fatal(e)
	}
	if e := pool.QueryRow(context.Background(), "SELECT count(*) FROM notification_events WHERE request_id=$1", id).Scan(&events); e != nil {
		t.Fatal(e)
	}
	if grants != 1 || events != 2 {
		t.Fatalf("grants=%d events=%d", grants, events)
	}
	expect(t, call(t, srv.URL, "POST", path, map[string]any{"version": 2, "action": "cancel"}, requester, origin), 200)
	countAssets(t, srv, requester, "/api/portal/assets", 0)
	if e := app.ExpireApprovals(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestR08PolicyLiveStorageAndGeoFailClosed(t *testing.T) {
	_, app, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("r08-policy", nil, nil), admin)
	ctx := context.Background()
	store := storage.New(app.db)
	root, e := store.Ensure(ctx, user)
	if e != nil {
		t.Fatal(e)
	}
	actor := storage.WithActor(ctx, user, "web", "", "192.0.2.10")
	file, e := store.Create(actor, user, root.ID, "before.txt", false)
	if e != nil {
		t.Fatal(e)
	}
	strategy := authorization.PolicyDocument{Name: "deny destructive", Enabled: true, UserIDs: []string{user}, Actions: map[string]bool{"delete": false, "rename": false, "edit": false, "createDir": false}, Timezone: "UTC", Mode: "blacklist"}
	id := postID(t, srv, "/api/admin/policies/strategy", strategy, admin)
	for name, fn := range map[string]func() error{"delete": func() error { return store.Remove(actor, user, file.ID) }, "rename": func() error { return store.Rename(actor, user, file.ID, root.ID, "after.txt") }, "write": func() error { return store.Write(actor, user, file.ID, 0, []byte("forbidden")) }, "truncate": func() error { return store.Resize(actor, user, file.ID, 0) }} {
		if e = fn(); !errors.Is(e, syscall.EACCES) {
			t.Fatalf("%s: %v", name, e)
		}
	}
	if _, e = store.Create(actor, user, root.ID, "denied", true); !errors.Is(e, syscall.EACCES) {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/policies/strategy/"+id+"?version=1", nil, admin, origin), 204)
	if e = store.Remove(actor, user, file.ID); e != nil {
		t.Fatal(e)
	}
	p := authorization.PolicyDocument{Name: "country gate", Enabled: true, UserIDs: []string{user}, Timezone: "UTC", Mode: "whitelist", Rules: []authorization.Rule{{Name: "local", Enabled: true, Action: "allow", Countries: []string{"TEST"}}}}
	postID(t, srv, "/api/admin/policies/access-policy", p, admin)
	facts := authorization.Facts{UserID: user, IP: "192.0.2.10", Action: "connect"}
	decision, e := app.authz.Evaluate(ctx, facts)
	if e != nil || decision.Allowed || decision.Reason != "geodata_unavailable" {
		t.Fatalf("missing geo: %+v %v", decision, e)
	}
	postID(t, srv, "/api/admin/geodata", map[string]any{"name": "internal fixture", "entries": []authorization.GeoEntry{{Prefix: "192.0.2.0/24", Country: "TEST"}}}, admin)
	decision, e = app.authz.Evaluate(ctx, facts)
	if e != nil || !decision.Allowed {
		t.Fatalf("imported geo: %+v %v", decision, e)
	}
}
