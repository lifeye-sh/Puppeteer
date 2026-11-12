package application

import (
	"context"
	"next-terminal/backend/internal/platform/secretbox"
	"testing"
)

func TestR12PersonalDatabasePasswordLifecycle(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	path := "/api/account/database-password"
	expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "POST", path, map[string]any{"password": "wrong", "days": 90}, admin, origin), 400)
	body := map[string]any{"password": testPassword, "days": 90}
	r := call(t, srv.URL, "POST", path, body, admin, origin)
	expect(t, r, 200)
	password := object(t, r)["password"].(string)
	user := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	var cipher, key []byte
	if e := p.QueryRow(context.Background(), `SELECT ciphertext,wrapped_key FROM database_passwords WHERE user_id=$1`, user).Scan(&cipher, &key); e != nil {
		t.Fatal(e)
	}
	verifier, e := secretbox.Unwrap(a.config.VaultKey, cipher, key, "database-password:"+user)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.databasePasswords.Verify(context.Background(), password, string(verifier)); e != nil {
		t.Fatal(e)
	}
	clear(verifier)
	if _, e = secretbox.Unwrap(a.config.VaultKey, cipher, key, "database-password:another-user"); e == nil {
		t.Fatal("user binding bypass")
	}
	rotated := call(t, srv.URL, "POST", path, body, admin, origin)
	expect(t, rotated, 200)
	if object(t, rotated)["password"] == password {
		t.Fatal("password reused")
	}
	if e = p.QueryRow(context.Background(), `SELECT ciphertext,wrapped_key FROM database_passwords WHERE user_id=$1`, user).Scan(&cipher, &key); e != nil {
		t.Fatal(e)
	}
	verifier, e = secretbox.Unwrap(a.config.VaultKey, cipher, key, "database-password:"+user)
	if e != nil {
		t.Fatal(e)
	}
	if a.databasePasswords.Verify(context.Background(), password, string(verifier)) == nil {
		t.Fatal("old password accepted")
	}
	clear(verifier)
	expect(t, call(t, srv.URL, "DELETE", path, body, admin, origin), 204)
	status := object(t, call(t, srv.URL, "GET", path, nil, admin, origin))
	if status["active"] != false {
		t.Fatal("revocation failed")
	}
	var size int
	if e = p.QueryRow(context.Background(), `SELECT octet_length(ciphertext)+octet_length(wrapped_key) FROM database_passwords WHERE user_id=$1`, user).Scan(&size); e != nil || size != 0 {
		t.Fatal("revoked verifier remains", e)
	}
}
