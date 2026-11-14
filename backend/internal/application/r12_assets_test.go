package application

import (
	"context"
	"next-terminal/backend/internal/modules/pki"
	"testing"
)

func TestR12DatabaseAssetScopeAndVersions(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	// Extending the registry must preserve R09 Web assets.
	if _, e := pool.Exec(context.Background(), `INSERT INTO resource_registry(kind,display_name) VALUES('website','existing Web asset')`); e != nil {
		t.Fatal(e)
	}
	user := postID(t, srv, "/api/admin/users", userInput("db-reader", nil, nil), admin)
	reader := loginAs(t, srv, "db-reader")
	credential := postID(t, srv, "/api/admin/credentials", credInput("database readonly"), admin)
	ca, key, e := pki.Generate("database fixture", []string{"db.internal"}, 2, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]any{"name": "Inventory", "alias": "inventory", "host": "db.internal", "port": 3306, "database": "inventory", "readonlyCredentialId": credential, "tlsServerName": "db.internal", "tlsCa": string(ca), "enabled": true, "connectTimeout": 10}
	const management = "/api/admin/database-assets"
	const portal = "/api/portal/database-assets"
	expect(t, call(t, srv.URL, "POST", management, input, reader, origin), 403)
	id := postID(t, srv, management, input, admin)
	countAssets(t, srv, reader, portal, 0)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": id}, admin)
	countAssets(t, srv, reader, portal, 1)
	result := object(t, call(t, srv.URL, "GET", portal, nil, reader, origin))["items"].([]any)[0].(map[string]any)
	if result["readonlyCredentialId"] != "" || result["tlsCa"] != "" {
		t.Fatal("credential metadata disclosed")
	}
	input["version"] = 1
	input["name"] = "Renamed inventory"
	expect(t, call(t, srv.URL, "PUT", management+"/"+id, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", management+"/"+id, input, admin, origin), 409)
	input["version"] = 2
	input["writeCredentialId"] = credential
	expect(t, call(t, srv.URL, "PUT", management+"/"+id, input, admin, origin), 422)
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	countAssets(t, srv, reader, portal, 0)
	expect(t, call(t, srv.URL, "DELETE", management+"/"+id+"?version=1", nil, admin, origin), 409)
	expect(t, call(t, srv.URL, "DELETE", management+"/"+id+"?version=2", nil, admin, origin), 204)
	countAssets(t, srv, admin, management, 0)
}

func TestDatabasePermissionModePersistence(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	credential := postID(t, srv, "/api/admin/credentials", credInput("mode credential"), admin)
	input := map[string]any{"name": "Mode asset", "alias": "mode_asset", "host": "db.internal", "port": 3306, "database": "inventory", "readonlyCredentialId": credential, "writeCredentialId": credential, "nativeWriteCredentialId": credential, "tlsMode": "disable", "enabled": true, "connectTimeout": 10, "permissionMode": "program"}
	const endpoint = "/api/admin/database-assets"
	id := postID(t, srv, endpoint, input, admin)
	check := func() {
		t.Helper()
		item := object(t, call(t, srv.URL, "GET", endpoint, nil, admin, origin))["items"].([]any)[0].(map[string]any)
		if item["permissionMode"] != "program" {
			t.Fatalf("mode not preserved: %v", item["permissionMode"])
		}
	}
	check()
	input["version"] = 1
	delete(input, "permissionMode")
	expect(t, call(t, srv.URL, "PUT", endpoint+"/"+id, input, admin, origin), 200)
	check()
	input["version"] = 2
	input["permissionMode"] = "account"
	expect(t, call(t, srv.URL, "PUT", endpoint+"/"+id, input, admin, origin), 422)
	input["permissionMode"] = "unknown"
	expect(t, call(t, srv.URL, "PUT", endpoint+"/"+id, input, admin, origin), 422)
	input["permissionMode"] = "account"
	input["writeCredentialId"] = ""
	input["nativeWriteCredentialId"] = ""
	expect(t, call(t, srv.URL, "PUT", endpoint+"/"+id, input, admin, origin), 200)
}
