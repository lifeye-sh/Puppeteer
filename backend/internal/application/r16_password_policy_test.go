package application

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/system"
	"testing"
)

func TestR16PasswordPolicyRealWritePaths(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	input := userInput("policy-member", nil, nil)
	input["password"] = "password1234"
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", input, admin, origin), 422)
	var count int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE username='policy-member'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("rejected password created user", e)
	}
	input["password"] = testPassword
	id := postID(t, srv, "/api/admin/users", input, admin)
	member := loginAs(t, srv, "policy-member")
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	policy := system.PasswordPolicy{MinLength: 8, MinCharacterType: 3, MustNotContainUsername: true, MustNotWeak: true}
	raw, _ := json.Marshal(policy)
	patch := map[string]any{"set": map[string]any{"password-strength-type": "customize", "password-strength-policy": string(raw)}}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, patch, member)
	expect(t, r, 403)
	r, tag = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, patch, admin)
	expect(t, r, 202)
	// Existing passwords still authenticate; configuration affects new values.
	loginAs(t, srv, "policy-member")
	input["version"] = 1
	input["password"] = "allletterswithoutclasses"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+id, input, admin, origin), 422)
	input["password"] = "Safe12!?"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+id, input, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/login", map[string]string{"username": "policy-member", "password": "Safe12!?"}, nil, origin), 200)
	// Invalid legacy and native batches do not partially publish either key.
	for _, changes := range []map[string]any{{"password-strength-type": "anything!"}, {"password-strength-policy": "{}", "system-name": "must-not-commit"}, {"password-strength-policy": map[string]any{"minLength": 8}}} {
		r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": changes}, admin)
		expect(t, r, 422)
	}
	r, _ = settingsRequest(t, srv.URL, "PUT", "/api/admin/properties", tag, map[string]any{"password-strength-type": "recommend"}, admin)
	expect(t, r, 204)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil || v.Values.String("password-strength-type") != "recommend" || v.Values.String("system-name") == "must-not-commit" {
		t.Fatal("configuration atomicity", e)
	}
}
