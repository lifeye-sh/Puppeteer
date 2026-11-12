package application

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestR13LDAPConfigurationAndPermissions(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("ldap-reader", nil, nil), admin)
	reader := loginAs(t, srv, "ldap-reader")
	path := "/api/admin/ldap-providers"
	in := map[string]any{"name": "isolated directory", "url": "ldaps://127.0.0.1:1", "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "bindPassword": "fixture-service-password", "password": testPassword}
	expect(t, call(t, srv.URL, "GET", path, nil, reader, origin), 403)
	expect(t, call(t, srv.URL, "POST", path, in, reader, origin), 403)
	id := postID(t, srv, path, in, admin)
	list := call(t, srv.URL, "GET", path, nil, admin, origin)
	expect(t, list, 200)
	if bytes.Contains(list.body, []byte(in["bindPassword"].(string))) {
		t.Fatal("LDAP password leaked")
	}
	var sealed []byte
	if e := pool.QueryRow(context.Background(), `SELECT secret_sealed FROM ldap_identity_providers WHERE id=$1`, id).Scan(&sealed); e != nil || bytes.Contains(sealed, []byte(in["bindPassword"].(string))) {
		t.Fatal("LDAP password not sealed", e)
	}
	in["version"] = 1
	in["bindPassword"] = ""
	in["name"] = "updated"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 200)
	var retained []byte
	if e := pool.QueryRow(context.Background(), `SELECT secret_sealed FROM ldap_identity_providers WHERE id=$1`, id).Scan(&retained); e != nil || !bytes.Equal(retained, sealed) {
		t.Fatal("blank password did not preserve credential", e)
	}
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 409)
	in["version"] = 2
	in["baseDN"] = "dc=other,dc=com"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 409)
	in["baseDN"] = "dc=example,dc=com"
	in["identityAttribute"] = "objectGUID"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 409)
	in["identityAttribute"] = "entryUUID"
	in["searchFilter"] = "(broken"
	expect(t, call(t, srv.URL, "PUT", path+"/"+id, in, admin, origin), 422)
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/test", map[string]string{}, reader, origin), 403)
	result := call(t, srv.URL, "POST", path+"/"+id+"/test", map[string]string{}, admin, origin)
	expect(t, result, 422)
	var body map[string]any
	if json.Unmarshal(result.body, &body) != nil || body["reason"] != "ldap_verification_failed" {
		t.Fatal("non-sanitized LDAP error")
	}
	if bytes.Contains(result.body, []byte("fixture-service-password")) {
		t.Fatal("error leaked credential")
	}
}
