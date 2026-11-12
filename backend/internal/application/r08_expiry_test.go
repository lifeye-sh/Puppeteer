package application

import (
	"context"
	"next-terminal/backend/internal/modules/authorization"
	"testing"
)

func TestR08CommandFilterEnforcedOnSSH(t *testing.T) {
	host, port, _ := sshTarget(t)
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("filtered-user", nil, nil), admin)
	user := loginAs(t, srv, "filtered-user")
	asset := r04Asset(t, srv, admin, host, port, true)
	filter := postID(t, srv, "/api/admin/policies/command-filter", map[string]any{"name": "whoami only", "enabled": true, "mode": "whitelist", "patterns": []map[string]string{{"pattern": "^whoami$", "action": "allow"}}}, admin)
	postID(t, srv, "/api/admin/grants", map[string]string{"userId": uid, "resourceId": asset, "commandFilterId": filter}, admin)
	session, ticket := reserve(t, srv, user, asset, false)
	ws := attach(t, srv, user, session, ticket)
	readUntil(t, ws, "SSH FIXTURE READY")
	if e := ws.WriteMessage(1, []byte("1rm -rf /tmp/not-run\r")); e != nil {
		t.Fatal(e)
	}
	readUntil(t, ws, "策略限制终端输入")
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+session+"/commands", map[string]string{"command": "id"}, user, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/sessions/"+session+"/commands", map[string]string{"command": "whoami"}, user, origin), 200)
	postID(t, srv, "/api/admin/policies/access-policy", authorization.PolicyDocument{Name: "live deny", Enabled: true, Global: false, UserIDs: []string{uid}, ResourceIDs: []string{asset}, Mode: "blacklist", Timezone: "UTC", Rules: []authorization.Rule{{Name: "deny", Enabled: true, Action: "reject"}}}, admin)
	waitClosed(t, a, session)
}

func TestR08ApprovedSSHConnectionExpires(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("expiry-operator", nil, nil), admin)
	user := loginAs(t, srv, "expiry-operator")
	asset := r04Asset(t, srv, admin, host, port, true)
	adminInfo := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))
	scope := postID(t, srv, "/api/admin/approval-scopes", map[string]any{"name": "expiry", "requesterIds": []string{uid}, "reviewerIds": []string{adminInfo["id"].(string)}, "resourceIds": []string{asset}, "enabled": true, "maxMinutes": 10}, admin)
	submitted := call(t, srv.URL, "POST", "/api/access-requests", map[string]any{"scopeId": scope, "resourceId": asset, "reason": "expiry verification", "durationMinutes": 1}, user, origin)
	expect(t, submitted, 201)
	id := object(t, submitted)["id"].(string)
	expect(t, call(t, srv.URL, "POST", "/api/access-requests/"+id+"/decision", map[string]any{"version": 1, "action": "approve"}, admin, origin), 200)
	session, ticket := reserve(t, srv, user, asset, false)
	ws := attach(t, srv, user, session, ticket)
	readUntil(t, ws, "SSH FIXTURE READY")
	_, e := pool.Exec(context.Background(), "UPDATE grants SET valid_from=now()-interval '2 minutes',expires_at=now()-interval '1 second' WHERE approval_id=$1", id)
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(context.Background(), "UPDATE access_requests SET starts_at=now()-interval '2 minutes',expires_at=now()-interval '1 second' WHERE id=$1", id)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.ExpireApprovals(context.Background()); e != nil {
		t.Fatal(e)
	}
	countAssets(t, srv, user, "/api/portal/assets", 0)
	waitClosed(t, a, session)
	var status string
	var count int
	if e = pool.QueryRow(context.Background(), "SELECT status FROM access_requests WHERE id=$1", id).Scan(&status); e != nil || status != "expired" {
		t.Fatal(status, e)
	}
	if e = a.ExpireApprovals(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(context.Background(), "SELECT count(*) FROM notification_events WHERE request_id=$1 AND event_type='access.expired'", id).Scan(&count); e != nil || count != 1 {
		t.Fatal("expiry event", count, e)
	}
}
