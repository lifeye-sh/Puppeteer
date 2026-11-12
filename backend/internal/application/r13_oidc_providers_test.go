package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestR13OIDCProviderConfiguration(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("oidc-reader", nil, nil), admin)
	reader := loginAs(t, srv, "oidc-reader")
	path := "/api/admin/oidc-providers"
	in := map[string]any{"name": "isolated IdP", "issuer": "https://idp.example.com/tenant", "clientId": "nt-test", "redirectUri": "https://backend.localhost:5174/api/oidc/callback", "clientSecret": "isolated-secret-not-for-production", "password": testPassword}
	expect(t, call(t, srv.URL, "POST", path, in, reader, origin), 403)
	id := postID(t, srv, path, in, admin)
	list := object(t, call(t, srv.URL, "GET", path, nil, admin, origin))
	data, _ := json.Marshal(list)
	if strings.Contains(string(data), in["clientSecret"].(string)) {
		t.Fatal("secret leaked")
	}
	var sealed []byte
	if e := pool.QueryRow(context.Background(), `SELECT secret_sealed FROM oidc_identity_providers WHERE id=$1`, id).Scan(&sealed); e != nil || strings.Contains(string(sealed), in["clientSecret"].(string)) {
		t.Fatal("secret not encrypted", e)
	}
	in["version"] = 1
	in["clientSecret"] = ""
	in["name"] = "updated"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 409)
	in["version"] = 2
	in["issuer"] = "https://other.example.com"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 409)
	in["issuer"] = "https://idp.example.com/tenant"
	in["enabled"] = true
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 200)
	in["enabled"] = false
	in["redirectUri"] = "http://backend.localhost:5174/callback"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 422)
}
