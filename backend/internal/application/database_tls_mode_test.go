package application

import (
	"context"
	"next-terminal/backend/internal/modules/database"
	"os"
	"testing"
)

func TestDatabaseTLSModePersistence(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cred := credInput("no TLS reader")
	cred["username"] = "reader"
	cred["secret"] = map[string]string{"password": "fixture-reader-only"}
	credential := postID(t, srv, "/api/admin/credentials", cred, admin)
	for _, engine := range []string{"mysql", "postgresql"} {
		host, port := "puppeteer-no-tls-mysql", 3306
		if engine == "postgresql" {
			host = "puppeteer-no-tls-postgres"
			port = 5432
		}
		body := map[string]any{"engine": engine, "name": "No TLS " + engine, "alias": "plain_" + engine, "host": host, "port": port, "database": "inventory", "readonlyCredentialId": credential, "enabled": true, "connectTimeout": 10, "tlsMode": "disable"}
		id := postID(t, srv, "/api/admin/database-assets", body, admin)
		asset, e := database.New(a.db).Asset(context.Background(), id)
		if e != nil || asset.TLSMode != "disable" {
			t.Fatal(asset, e)
		}
		byAlias, e := database.New(a.db).AssetByAlias(context.Background(), "plain_"+engine)
		if e != nil || byAlias.TLSMode != "disable" {
			t.Fatal("native target mode lost", e)
		}
		body["tlsMode"] = ""
		body["version"] = 1
		expect(t, call(t, srv.URL, "PUT", "/api/admin/database-assets/"+id, body, admin, origin), 422)
		body["tlsMode"] = "prefer"
		expect(t, call(t, srv.URL, "PUT", "/api/admin/database-assets/"+id, body, admin, origin), 422)
		if os.Getenv("NT_TEST_NO_TLS") == "1" {
			response := call(t, srv.URL, "POST", "/api/admin/database-assets/"+id+"/test", nil, admin, origin)
			expect(t, response, 200)
			result := object(t, response)
			items := result["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["ok"] != true {
				t.Fatalf("diagnostic failed %s", response.body)
			}
		}
	}
}
