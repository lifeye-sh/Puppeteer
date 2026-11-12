package application

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestR13LDAPDirectorySync(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("sync-outsider", nil, nil), admin)
	outsider := loginAs(t, srv, "sync-outsider")
	address, ca := ldapLifecycleServer(t)
	input := map[string]any{"name": "sync fixture", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "enabled": true, "bindingEnabled": true, "password": testPassword}
	pid := postID(t, srv, "/api/admin/ldap-providers", input, admin)
	department := postID(t, srv, "/api/admin/departments", map[string]string{"name": "Directory imports"}, admin)
	preview := func() string {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		r := call(t, srv.URL, "POST", "/api/admin/ldap-providers/"+pid+"/sync-preview", map[string]any{"password": testPassword, "limit": 1000}, admin, origin)
		expect(t, r, 200)
		data := object(t, r)
		entries := data["entries"].([]any)
		if len(entries) != 1 || entries[0].(map[string]any)["username"] != "alice" {
			t.Fatal("directory preview mismatch")
		}
		return data["preview"].(string)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-providers/"+pid+"/sync-preview", map[string]any{"password": testPassword, "limit": 1000}, outsider, origin), 403)
	token := preview()
	body := map[string]any{"preview": token, "subjects": []string{"unlisted"}, "departmentId": department, "password": testPassword}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin), 422)
	body["subjects"] = []string{"entryUUID:9d20fd4b-68b4-48cd-98fd-2e1394166a7f"}
	r := call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin)
	expect(t, r, 200)
	if object(t, r)["created"] != float64(1) {
		t.Fatal("import missing")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin), 400)
	var uid, status, kind, username, hash string
	if e := pool.QueryRow(ctx, `SELECT u.id,u.status,u.type,u.username,u.password_hash FROM users u JOIN ldap_identities e ON e.user_id=u.id WHERE e.provider_id=$1`, pid).Scan(&uid, &status, &kind, &username, &hash); e != nil {
		t.Fatal(e)
	}
	if status != "disabled" || kind != "user" || hash != "!external-only" {
		t.Fatal("directory import granted access")
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM user_departments WHERE user_id=$1 AND department_id=$2`, uid, department).Scan(&count)
	if count != 1 {
		t.Fatal("default department missing")
	}
	body["preview"] = preview()
	r = call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin)
	expect(t, r, 200)
	if object(t, r)["existing"] != float64(1) {
		t.Fatal("not idempotent")
	}
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	login := map[string]string{"source": "ldap", "providerId": pid, "username": "alice", "password": "user-password"}
	expect(t, call(t, srv.URL, "POST", "/api/ldap/login", login, nil, secure), 400)
	pool.Exec(ctx, `UPDATE users SET status='enabled' WHERE id=$1`, uid)
	expect(t, call(t, srv.URL, "POST", "/api/ldap/login", login, nil, secure), 200)
	departed := postID(t, srv, "/api/admin/users", userInput("departed-directory-user", nil, nil), admin)
	departedSession := loginAs(t, srv, "departed-directory-user")
	manual := postID(t, srv, "/api/admin/users", userInput("manual-directory-user", nil, nil), admin)
	if _, e := pool.Exec(ctx, `INSERT INTO ldap_identities(provider_id,user_id,subject,managed) VALUES($1,$2,'entryUUID:missing-managed',true),($1,$3,'entryUUID:missing-manual',false)`, pid, departed, manual); e != nil {
		t.Fatal(e)
	}
	body["preview"] = preview()
	body["disableMissing"] = true
	r = call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin)
	expect(t, r, 200)
	if object(t, r)["disabled"] != float64(1) {
		t.Fatal("offboarding missing")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, departedSession, origin), 401)
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, manual).Scan(&status)
	if status != "enabled" {
		t.Fatal("manual binding overwritten")
	}
	body["preview"] = preview()
	pool.Exec(ctx, `UPDATE ldap_sync_previews SET expires_at=now()-interval '1 second'`)
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin), 400)
	body["preview"] = preview()
	secondBrowser := loginAs(t, srv, "accept-admin")
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, secondBrowser, origin), 400)
	input["version"] = 1
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin), 400)
}
func TestR13LoginPolicyAdminProtection(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	admin := loginAs(t, srv, "accept-admin")
	inputUser := userInput("alternate-admin", nil, nil)
	inputUser["type"] = "admin"
	postID(t, srv, "/api/admin/users", inputUser, admin)
	second := loginAs(t, srv, "alternate-admin")
	policy := map[string]any{"passwordEnabled": false, "version": 1, "password": testPassword}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/login-policy", policy, admin, origin), 409)
	address, ca := ldapLifecycleServer(t)
	provider := map[string]any{"name": "admin directory", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "enabled": true, "bindingEnabled": true, "password": testPassword}
	pid := postID(t, srv, "/api/admin/ldap-providers", provider, admin)
	bind := func(id string, c *http.Cookie) string {
		expect(t, call(t, srv.URL, "POST", "/api/account/ldap-providers/"+id+"/bind", map[string]string{"username": "alice", "directoryPassword": "user-password", "password": testPassword}, c, secure), 200)
		var binding string
		pool.QueryRow(ctx, `SELECT id FROM ldap_identities WHERE provider_id=$1`, id).Scan(&binding)
		return binding
	}
	eid := bind(pid, admin)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/login-policy", policy, admin, origin), 409)
	login := func(id string) {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		expect(t, call(t, srv.URL, "POST", "/api/ldap/login", map[string]string{"source": "ldap", "providerId": id, "username": "alice", "password": "user-password"}, nil, secure), 200)
	}
	login(pid)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/login-policy", policy, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": testPassword}, nil, origin), 403)
	provider["version"] = 1
	provider["enabled"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, provider, admin, origin), 409)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/ldap-identities/"+eid, map[string]string{"password": testPassword}, admin, secure), 409)
	login(pid)
	provider["name"] = "second admin directory"
	provider["version"] = 0
	provider["enabled"] = true
	pid2 := postID(t, srv, "/api/admin/ldap-providers", provider, admin)
	eid2 := bind(pid2, second)
	login(pid2)
	a.mu.Lock()
	a.attempts = map[string]attempt{}
	a.mu.Unlock()
	out := make(chan int, 2)
	for _, pair := range []struct {
		id string
		c  *http.Cookie
	}{{eid, admin}, {eid2, second}} {
		go func(id string, c *http.Cookie) {
			out <- call(t, srv.URL, "DELETE", "/api/account/ldap-identities/"+id, map[string]string{"password": testPassword}, c, secure).status
		}(pair.id, pair.c)
	}
	success, blocked := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case status := <-out:
			if status == 200 {
				success++
			} else if status == 409 {
				blocked++
			} else {
				t.Fatal(status)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent admin invariant deadlock")
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatal("both alternate logins removed")
	}
	n, e := a.identity.AdminLoginAlternatives(ctx, "", false, "")
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e = a.EnablePasswordLoginFromCLI(ctx, "isolated-test-operator", "recover alternate identity outage"); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "accept-admin", "password": testPassword}, nil, origin), 200)
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM operation_logs WHERE action='identity.password_login_recovered'`).Scan(&audits)
	if audits != 1 {
		t.Fatal("recovery audit missing")
	}
	if e = a.db.Within(ctx, func(ctx context.Context) error { return a.identity.QuarantineRestore(ctx) }); e != nil {
		t.Fatal(e)
	}
	state, e := a.identity.LoginPolicy(ctx)
	if e != nil || !state.PasswordEnabled {
		t.Fatal("restore locked local login")
	}
}
