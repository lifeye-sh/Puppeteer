package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/secretbox"
	"strconv"
	"strings"
	"testing"
	"time"
)

func credInput(name string) map[string]any {
	return map[string]any{"name": name, "kind": "password", "username": "tester", "secret": map[string]string{"password": "remote-only-secret-R03"}}
}
func assetInput(name, credential, group string) map[string]any {
	return map[string]any{"name": name, "protocol": "ssh", "alias": "", "host": "192.0.2.10", "port": 22, "groupId": group, "credentialId": credential, "enabled": true, "connectTimeout": 10, "tags": []string{"test"}}
}
func countAssets(t *testing.T, srv *httptest.Server, c *http.Cookie, path string, want int) {
	t.Helper()
	r := call(t, srv.URL, "GET", path, nil, c, origin)
	expect(t, r, 200)
	if n := int(object(t, r)["total"].(float64)); n != want {
		t.Fatalf("asset count %d want %d: %s", n, want, r.body)
	}
}
func TestR03ResourceScopeAndGroupChanges(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	dept := postID(t, srv, "/api/admin/departments", map[string]any{"name": "R03 operations"}, admin)
	user := postID(t, srv, "/api/admin/users", userInput("asset-operator", nil, []string{dept}), admin)
	postID(t, srv, "/api/admin/users", userInput("asset-denied", nil, nil), admin)
	op := loginAs(t, srv, "asset-operator")
	denied := loginAs(t, srv, "asset-denied")
	credential := postID(t, srv, "/api/admin/credentials", credInput("shared"), admin)
	group := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "parent"}, admin)
	child := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "child", "parentId": group}, admin)
	a := postID(t, srv, "/api/admin/assets", assetInput("SSH A", credential, child), admin)
	b := postID(t, srv, "/api/admin/assets", assetInput("SSH B", credential, ""), admin)
	direct := postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": a}, admin)
	countAssets(t, srv, op, "/api/portal/assets", 1)
	countAssets(t, srv, op, "/api/portal/assets?search=SSH%20B", 0)
	countAssets(t, srv, denied, "/api/portal/assets", 0)
	expect(t, call(t, srv.URL, "GET", "/api/portal/assets/"+b, nil, op, origin), 403)
	r := call(t, srv.URL, "GET", "/api/portal/assets/"+a, nil, op, origin)
	expect(t, r, 200)
	if bytes.Contains(r.body, []byte("credentialId")) {
		t.Fatal("credential reference leaked to portal")
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/assets/export", nil, op, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets", assetInput("bad", credential, ""), op, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+direct, nil, admin, origin), 204)
	countAssets(t, srv, op, "/api/portal/assets", 0)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"departmentId": dept, "resourceId": group}, admin)
	countAssets(t, srv, op, "/api/portal/assets?tag=test&pageSize=1", 1)
	facets := object(t, call(t, srv.URL, "GET", "/api/portal/facets", nil, op, origin))
	if len(facets["groups"].([]any)) != 1 {
		t.Fatal("unexpected authorized facets", facets)
	}
	emptyFacets := object(t, call(t, srv.URL, "GET", "/api/portal/facets", nil, denied, origin))
	if len(emptyFacets["groups"].([]any)) != 0 || len(emptyFacets["tags"].([]any)) != 0 {
		t.Fatal("facets leak", emptyFacets)
	}
	disabled := assetInput("SSH A", credential, child)
	disabled["enabled"] = false
	disabled["version"] = 1
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+a, disabled, admin, origin), 200)
	countAssets(t, srv, op, "/api/portal/assets", 0)
	countAssets(t, srv, admin, "/api/portal/assets", 1)
	disabled["enabled"] = true
	disabled["version"] = 2
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+a, disabled, admin, origin), 200)

	expect(t, call(t, srv.URL, "PUT", "/api/admin/resource-groups/"+group, map[string]any{"name": "parent", "parentId": child, "version": 1}, admin, origin), 422)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/resource-groups/"+group, nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/resource-groups/"+child, map[string]any{"name": "child", "parentId": "", "version": 1}, admin, origin), 200)
	countAssets(t, srv, op, "/api/portal/assets", 0)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/resource-groups/"+child, map[string]any{"name": "child", "parentId": group, "version": 2}, admin, origin), 200)
	countAssets(t, srv, op, "/api/portal/assets", 1)
	expired := map[string]any{"departmentId": dept, "resourceId": group, "version": 1, "validFrom": time.Now().Add(-2 * time.Hour), "expiresAt": time.Now().Add(-time.Hour)}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/grants/"+grant, expired, admin, origin), 200)
	countAssets(t, srv, op, "/api/portal/assets", 0)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/grants/"+grant, expired, admin, origin), 409)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/credentials/"+credential, nil, admin, origin), 409)
}
func TestR03CredentialVersionsRevealAndTampering(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	id := postID(t, srv, "/api/admin/credentials", credInput("password"), admin)
	r := call(t, srv.URL, "GET", "/api/admin/credentials", nil, admin, origin)
	expect(t, r, 200)
	if bytes.Contains(r.body, []byte("remote-only-secret")) {
		t.Fatal("secret leaked")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/credentials/"+id+"/reveal", map[string]string{"password": testPassword, "totp": "000000"}, admin, origin), 403)
	var ciphertext, wrapped []byte
	if e := p.QueryRow(context.Background(), `SELECT ciphertext,wrapped_dek FROM secret_versions WHERE credential_id=$1 AND version=1`, id).Scan(&ciphertext, &wrapped); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(ciphertext, []byte("remote-only-secret")) {
		t.Fatal("plaintext stored")
	}
	if _, e := secretbox.Unwrap(a.config.VaultKey, ciphertext, wrapped, "credential:wrong:1"); e == nil {
		t.Fatal("AAD swap accepted")
	}
	wrapped[0] ^= 1
	if _, e := secretbox.Unwrap(a.config.VaultKey, ciphertext, wrapped, "credential:"+id+":1"); e == nil {
		t.Fatal("tampered DEK accepted")
	}
	input := credInput("password")
	input["version"] = 1
	input["secret"] = map[string]string{"password": "changed-R03-secret"}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/credentials/"+id, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/credentials/"+id, input, admin, origin), 409)
	delete(input, "secret")
	input["version"] = 2
	input["username"] = "updated-user"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/credentials/"+id, input, admin, origin), 200)
	var versions, active int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM secret_versions WHERE credential_id=$1`, id).Scan(&versions); e != nil || versions != 2 {
		t.Fatal(versions, e)
	}
	if e := p.QueryRow(context.Background(), `SELECT active_version FROM credentials WHERE id=$1`, id).Scan(&active); e != nil || active != 2 {
		t.Fatal(active, e)
	}
	secret, _, bound := enroll(t, srv, admin)
	code := identity.TOTP(secret, time.Now().Unix()/30+1)
	expect(t, call(t, srv.URL, "POST", "/api/admin/credentials/"+id+"/reveal", map[string]string{"password": "wrong", "totp": code}, bound, origin), 400)
	r = call(t, srv.URL, "POST", "/api/admin/credentials/"+id+"/reveal", map[string]string{"password": testPassword, "totp": code}, bound, origin)
	expect(t, r, 200)
	if object(t, r)["password"] != "changed-R03-secret" {
		t.Fatal("wrong secret")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/credentials/"+id+"/reveal", map[string]string{"password": testPassword, "totp": code}, bound, origin), 400)
	var events int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM secret_access_events WHERE credential_id=$1`, id).Scan(&events); e != nil || events != 1 {
		t.Fatal(events, e)
	}
	keyID := postID(t, srv, "/api/admin/credentials", map[string]any{"name": "generated", "kind": "private-key", "username": "tester", "generate": true}, bound)
	meta, e := a.vault.Get(context.Background(), keyID)
	if e != nil || !strings.HasPrefix(meta.PublicKey, "ssh-ed25519 ") {
		t.Fatal("public key", e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/credentials", map[string]any{"name": "invalid", "kind": "private-key", "username": "tester", "secret": map[string]string{"privateKey": "not-a-key"}}, bound, origin), 422)
}
func TestR03ImportExportAtomicRowsAndReferences(t *testing.T) {
	p, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	c := postID(t, srv, "/api/admin/credentials", credInput("import shared"), admin)
	valid := assetInput("import A", c, "")
	invalid := assetInput("invalid", c, "")
	invalid["port"] = 0
	r := call(t, srv.URL, "POST", "/api/admin/assets/import", map[string]any{"items": []any{valid, invalid, valid}}, admin, origin)
	expect(t, r, 200)
	v := object(t, r)
	if v["succeeded"].(float64) != 1 || v["failed"].(float64) != 2 {
		t.Fatal(string(r.body))
	}
	var registries int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM resource_registry WHERE kind='asset'`).Scan(&registries); e != nil || registries != 1 {
		t.Fatal("orphan registry after failed row", registries, e)
	}
	r = call(t, srv.URL, "GET", "/api/admin/assets/export", nil, admin, origin)
	expect(t, r, 200)
	if bytes.Contains(r.body, []byte("remote-only-secret")) || bytes.Contains(r.body, []byte("password")) {
		t.Fatal("secret exported")
	}
	var exported struct {
		Items []map[string]any `json:"items"`
	}
	if e := json.Unmarshal(r.body, &exported); e != nil {
		t.Fatal(e)
	}
	exported.Items[0]["name"] = "roundtrip B"
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/import", exported, admin, origin), 200)
	countAssets(t, srv, admin, "/api/admin/assets/paging", 2)
	all := object(t, call(t, srv.URL, "GET", "/api/admin/assets/paging", nil, admin, origin))["items"].([]any)
	id := all[0].(map[string]any)["id"].(string)
	update := assetInput("import A", c, "")
	update["version"] = 1
	update["enabled"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, update, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, update, admin, origin), 409)
	for _, item := range all {
		expect(t, call(t, srv.URL, "DELETE", "/api/admin/assets/"+item.(map[string]any)["id"].(string), nil, admin, origin), 204)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/credentials/"+c, nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets", assetInput("deleted cred", c, ""), admin, origin), 404)
}
func TestR03ProbeIsTCPOnlyAndRestricted(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	c := postID(t, srv, "/api/admin/credentials", credInput("probe"), admin)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	in := assetInput("TCP only", c, "")
	in["host"] = "127.0.0.1"
	in["port"] = n
	id := postID(t, srv, "/api/admin/assets", in, admin)
	r := call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/probe", nil, admin, origin)
	expect(t, r, 200)
	if object(t, r)["reachable"] != true || object(t, r)["kind"] != "tcp" {
		t.Fatal(string(r.body))
	}
	listener.Close()
	r = call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/probe", nil, admin, origin)
	expect(t, r, 200)
	if object(t, r)["reachable"] != false {
		t.Fatal("closed TCP claimed reachable")
	}
	in["version"] = 1
	in["host"] = "169.254.169.254"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, in, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/probe", nil, admin, origin), 422)
}

func TestR03FixtureIdempotence(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	if e := a.SeedR02(ctx, "accept-admin", []FixtureUser{{"operator", testPassword}, {"reviewer", testPassword}, {"denied", testPassword}}); e != nil {
		t.Fatal(e)
	}
	first, e := a.SeedR03(ctx, "accept-admin")
	if e != nil {
		t.Fatal(e)
	}
	second, e := a.SeedR03(ctx, "accept-admin")
	if e != nil || first["R03 SSH A"] != second["R03 SSH A"] {
		t.Fatal(e)
	}
	countAssets(t, srv, loginAs(t, srv, "operator"), "/api/portal/assets", 1)
	countAssets(t, srv, loginAs(t, srv, "denied"), "/api/portal/assets", 0)
	if e = a.system.CheckVaultKey(ctx, identity.Digest("vault-key-a")); e != nil {
		t.Fatal(e)
	}
	if e = a.system.CheckVaultKey(ctx, identity.Digest("vault-key-b")); e == nil {
		t.Fatal("changed vault key accepted")
	}
}
