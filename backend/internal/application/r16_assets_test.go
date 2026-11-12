package application

import (
	"next-terminal/backend/internal/modules/pki"
	"os"
	"path/filepath"
	"testing"
)

func TestR16PostgresDiagnosticsAPI(t *testing.T) {
	dir := os.Getenv("NT_R16_PG_FIXTURE")
	if dir == "" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ca, e := os.ReadFile(filepath.Join(dir, "server.pem"))
	if e != nil {
		t.Fatal(e)
	}
	credential := credInput("R16 isolated reader")
	credential["username"] = "reader"
	credential["secret"] = map[string]string{"password": "r16-fixture-reader"}
	idCredential := postID(t, srv, "/api/admin/credentials", credential, admin)
	input := map[string]any{"engine": "postgresql", "name": "PG diagnostic", "alias": "pgdiagnostic", "host": "r16-postgres-fixture", "port": 5432, "database": "inventory", "readonlyCredentialId": idCredential, "tlsServerName": "r16-postgres-fixture", "tlsCa": string(ca), "enabled": true, "connectTimeout": 5}
	id := postID(t, srv, "/api/admin/database-assets", input, admin)
	probe := func() map[string]any {
		return object(t, call(t, srv.URL, "POST", "/api/admin/database-assets/"+id+"/test", map[string]any{}, admin, origin))["items"].([]any)[0].(map[string]any)
	}
	if result := probe(); result["ok"] != true {
		t.Fatal(result)
	}
	input["version"] = 1
	input["tlsServerName"] = "wrong.internal"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/database-assets/"+id, input, admin, origin), 200)
	if result := probe(); result["ok"] != false || result["message"] != "TLS 服务器名称不匹配，请核对证书 SAN 与资产 TLS 名称" {
		t.Fatal("TLS diagnostic", result)
	}
}

func TestR16DatabaseEngineIsolationAndPasswordStatus(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cred := postID(t, srv, "/api/admin/credentials", credInput("R16 PG reader"), admin)
	ca, key, e := pki.Generate("PG asset", []string{"pg.internal"}, 1, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]any{"engine": "postgresql", "name": "R16 PostgreSQL", "alias": "r16pg", "host": "pg.internal", "port": 5432, "database": "inventory", "readonlyCredentialId": cred, "tlsServerName": "pg.internal", "tlsCa": string(ca), "enabled": true, "connectTimeout": 5}
	id := postID(t, srv, "/api/admin/database-assets", input, admin)
	input["version"] = 1
	input["engine"] = "mysql"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/database-assets/"+id, input, admin, origin), 409)
	delete(input, "engine") // Legacy editor must preserve persisted engine.
	expect(t, call(t, srv.URL, "PUT", "/api/admin/database-assets/"+id, input, admin, origin), 200)
	got := object(t, call(t, srv.URL, "GET", "/api/portal/database-assets", nil, admin, origin))["items"].([]any)[0].(map[string]any)
	if got["engine"] != "postgresql" || got["tlsCa"] != "" || got["readonlyCredentialId"] != "" {
		t.Fatal("engine/portal redaction", got)
	}
	expect(t, call(t, srv.URL, "POST", "/api/database-work-orders", map[string]any{"assetId": id, "title": "PG must not use MySQL executor", "sql": "SELECT 1"}, admin, origin), 409)
	input["version"] = 2
	input["engine"] = "oracle"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/database-assets/"+id, input, admin, origin), 422)
	expect(t, call(t, srv.URL, "POST", "/api/account/database-password", map[string]any{"password": testPassword, "days": 1}, admin, origin), 200)
	status := object(t, call(t, srv.URL, "GET", "/api/account/database-password", nil, admin, origin))
	if status["scramReady"] != true {
		t.Fatal("new password missing SCRAM")
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/account/database-password", map[string]any{"password": testPassword}, admin, origin), 204)
	status = object(t, call(t, srv.URL, "GET", "/api/account/database-password", nil, admin, origin))
	if status["scramReady"] != false {
		t.Fatal("revoked SCRAM remains ready")
	}
}
