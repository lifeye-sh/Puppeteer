package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"golang.org/x/crypto/ssh"
	"io"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"strings"
	"testing"
)

func TestR13PendingMFARejectsCredentialChanges(t *testing.T) {
	pool, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	input := map[string]any{"name": "phase guard", "password": testPassword, "days": 30, "scopes": []string{"profile.read"}}
	id := object(t, call(t, srv.URL, "POST", "/api/account/api-keys", input, admin, origin))["id"].(string)
	for _, phase := range []string{"mfa_pending", "mfa_enroll"} {
		if _, e := pool.Exec(context.Background(), `UPDATE auth_sessions SET phase=$2 WHERE token_hash=$1`, identity.Digest(admin.Value), phase); e != nil {
			t.Fatal(e)
		}
		expect(t, call(t, srv.URL, "POST", "/api/account/api-keys", input, admin, origin), 403)
		expect(t, call(t, srv.URL, "DELETE", "/api/account/api-keys/"+id, map[string]string{"password": testPassword}, admin, origin), 403)
	}
	var count int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM personal_api_keys WHERE revoked_at IS NULL`).Scan(&count); e != nil || count != 1 {
		t.Fatal("pending MFA mutated credentials", e, count)
	}
}

func TestR13PersonalCredentialScopesAndRevocation(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("r13-reader", nil, nil), admin)
	reader := loginAs(t, srv, "r13-reader")
	path := "/api/account/api-keys"
	expect(t, call(t, srv.URL, "POST", path, map[string]any{"name": "no proof", "days": 30, "scopes": []string{"profile.read"}}, admin, origin), 400)
	input := map[string]any{"name": "read integration", "password": testPassword, "days": 30, "scopes": []string{"profile.read"}}
	created := object(t, call(t, srv.URL, "POST", path, input, admin, origin))
	id := created["id"].(string)
	secret := created["secret"].(string)
	if !strings.HasPrefix(secret, "ntk_") || len(secret) != 68 {
		t.Fatal("invalid key format")
	}
	var digest string
	if e := pool.QueryRow(context.Background(), `SELECT token_hash FROM personal_api_keys WHERE id=$1`, id).Scan(&digest); e != nil || strings.Contains(digest, secret) {
		t.Fatal("key storage", e)
	}
	list := object(t, call(t, srv.URL, "GET", path, nil, admin, origin))
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), digest) {
		t.Fatal("secret material returned in list")
	}
	bearer := func(target, key string) result {
		t.Helper()
		r, _ := http.NewRequest("GET", srv.URL+target, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		r.AddCookie(admin)
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return result{status: response.StatusCode, body: body}
	}
	expect(t, bearer("/api/account/info", secret), 200)
	expect(t, bearer("/api/portal/database-assets", secret), 401)
	expect(t, bearer("/api/admin/users", secret), 403)
	expect(t, bearer("/api/account/api-keys", secret), 403)
	expect(t, bearer("/api/account/info", "wrong-token"), 401)
	expect(t, call(t, srv.URL, "DELETE", path+"/"+id, map[string]any{"password": testPassword}, reader, origin), 404)
	expect(t, call(t, srv.URL, "DELETE", path+"/"+id, map[string]any{"password": testPassword}, admin, origin), 204)
	expect(t, bearer("/api/account/info", secret), 401)
	// Expiry and recovery epoch are checked on every API call.
	created = object(t, call(t, srv.URL, "POST", path, input, admin, origin))
	id = created["id"].(string)
	secret = created["secret"].(string)
	_, _ = pool.Exec(context.Background(), `UPDATE personal_api_keys SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
	expect(t, bearer("/api/account/info", secret), 401)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewPublicKey(pub)
	sshInput := map[string]any{"name": "SSH identity", "password": testPassword, "days": 30, "publicKey": string(ssh.MarshalAuthorizedKey(key))}
	sshCreated := object(t, call(t, srv.URL, "POST", "/api/account/ssh-keys", sshInput, admin, origin))
	sshID := sshCreated["id"].(string)
	expect(t, call(t, srv.URL, "POST", "/api/account/ssh-keys", sshInput, admin, origin), 409)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/ssh-keys/"+sshID, map[string]any{"password": testPassword}, reader, origin), 404)
	created = object(t, call(t, srv.URL, "POST", path, input, admin, origin))
	secret = created["secret"].(string)
	if e := a.identity.QuarantineRestore(context.Background()); e != nil {
		t.Fatal(e)
	}
	expect(t, bearer("/api/account/info", secret), 401)
	var active int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM personal_ssh_keys WHERE revoked_at IS NULL`).Scan(&active); e != nil || active != 0 {
		t.Fatal("restore kept SSH identity", e)
	}
}
