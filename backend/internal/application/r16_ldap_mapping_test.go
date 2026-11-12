package application

import (
	"context"
	"next-terminal/backend/internal/modules/identity"
	"testing"
)

func TestR16LDAPMappingAndConfiguredLimit(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	address, ca := ldapLifecycleServer(t, map[string][]string{"cn": {"Alice 目录昵称"}, "mail": {"alice@example.com"}})
	input := map[string]any{"name": "mapped directory", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "enabled": true, "password": testPassword, "searchSizeLimit": 1, "propertyMapping": map[string]string{"username": "uid", "nickname": "cn", "mail": "mail"}}
	pid := postID(t, srv, "/api/admin/ldap-providers", input, admin)
	stored, e := a.identity.LDAPProvider(ctx, pid)
	if e != nil || stored.DirectoryLimit() != 1 || stored.PropertyMapping.Nickname != "cn" {
		t.Fatal(stored, e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-providers/"+pid+"/sync-preview", map[string]any{"password": testPassword, "limit": 2}, admin, origin), 422)
	// Omitted request limit takes the configured cap, even below the old 1000 default.
	preview := func() string {
		r := call(t, srv.URL, "POST", "/api/admin/ldap-providers/"+pid+"/sync-preview", map[string]any{"password": testPassword}, admin, origin)
		expect(t, r, 200)
		v := object(t, r)
		entry := v["entries"].([]any)[0].(map[string]any)
		if entry["nickname"] != "Alice 目录昵称" || entry["mail"] != "alice@example.com" || entry["username"] != "alice" {
			t.Fatal(entry)
		}
		return v["preview"].(string)
	}
	token := preview()
	body := map[string]any{"password": testPassword, "preview": token, "subjects": []string{"entryUUID:9d20fd4b-68b4-48cd-98fd-2e1394166a7f"}}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin), 200)
	var nickname, status, hash string
	var contacts int
	if e := pool.QueryRow(ctx, `SELECT nickname,status,password_hash FROM users u JOIN ldap_identities i ON i.user_id=u.id WHERE i.provider_id=$1`, pid).Scan(&nickname, &status, &hash); e != nil {
		t.Fatal(e)
	}
	if nickname != "Alice 目录昵称" || status != "disabled" || hash != "!external-only" {
		t.Fatal("import changed account security", nickname, status, hash)
	}
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM verified_contacts c JOIN ldap_identities i ON i.user_id=c.user_id WHERE i.provider_id=$1`, pid).Scan(&contacts); e != nil {
		t.Fatal(e)
	}
	if contacts != 0 {
		t.Fatal("directory email became account contact")
	}
	body["preview"] = preview()
	// Old callers editing only existing fields preserve the configured bounds/mapping.
	delete(input, "searchSizeLimit")
	delete(input, "propertyMapping")
	delete(input, "bindPassword")
	input["version"] = 1
	input["name"] = "renamed"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 200)
	stored, e = a.identity.LDAPProvider(ctx, pid)
	if e != nil || stored.DirectoryLimit() != 1 || stored.PropertyMapping.Nickname != "cn" {
		t.Fatal("old client erased mapping", e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ldap-sync", body, admin, origin), 400)
	input["version"] = stored.Version
	for _, bad := range []any{0, -1, 10001} {
		input["searchSizeLimit"] = bad
		expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 422)
	}
	input["searchSizeLimit"] = 1
	for _, bad := range []map[string]string{{"username": "mail"}, {"username": "uid", "nickname": "*"}} {
		input["propertyMapping"] = bad
		expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 422)
	}
	input["propertyMapping"] = map[string]string{"username": "uid", "unknown": "cn"}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 400)
	input["propertyMapping"] = map[string]string{"username": "uid"}
	input["searchSizeLimit"] = 1.5
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ldap-providers/"+pid, input, admin, origin), 400)
	// Ambiguous optional claims must reject the complete preview, never return partial rows.
	badAddress, badCA := ldapLifecycleServer(t, map[string][]string{"cn": {"Alice", "Bob"}})
	stored.URL = badAddress
	stored.CAPEM = badCA
	if entries, e := identity.ReadLDAPDirectory(ctx, stored, "service-password", 1); e == nil || entries != nil {
		t.Fatal("ambiguous claims accepted", entries, e)
	}
}
