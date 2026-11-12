package application

import (
	"context"
	"encoding/base32"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"testing"
	"time"
)

func TestR16ExternalEnrollmentProofScope(t *testing.T) {
	for _, u := range []*identity.User{nil, {}, {Phase: "authenticated", ExternalIdentityID: "external"}, {Phase: "mfa_pending", LDAPIdentityID: "directory"}, {Phase: "mfa_enroll"}, {Phase: "mfa_enroll", ContactID: "sms"}, {Phase: "mfa_enroll", PasskeyID: "passkey"}} {
		if externalMFAEnrollment(u) {
			t.Fatal("accepted unproven enrollment phase", u)
		}
	}
	for _, u := range []*identity.User{{Phase: "mfa_enroll", ExternalIdentityID: "external"}, {Phase: "mfa_enroll", LDAPIdentityID: "directory"}} {
		if !externalMFAEnrollment(u) {
			t.Fatal("verified external primary not recognized")
		}
	}
}

func TestR16LDAPEnrollmentWithoutLocalPassword(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("external-enroll", nil, nil), admin)
	owner := loginAs(t, srv, "external-enroll")
	address, ca := ldapLifecycleServer(t)
	pid := postID(t, srv, "/api/admin/ldap-providers", map[string]any{"name": "MFA directory", "url": address, "caPEM": ca, "baseDN": "dc=example,dc=com", "bindDN": "cn=service,dc=example,dc=com", "bindPassword": "service-password", "searchFilter": "(objectClass=person)", "loginAttribute": "uid", "identityAttribute": "entryUUID", "enabled": true, "bindingEnabled": true, "password": testPassword}, admin)
	expect(t, call(t, srv.URL, "POST", "/api/account/ldap-providers/"+pid+"/bind", map[string]string{"username": "alice", "directoryPassword": "user-password", "password": testPassword}, owner, secure), 200)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	// External-only accounts have no local password verifier.
	exec(`UPDATE users SET password_hash='' WHERE id=$1`, uid)
	login := func() *http.Cookie {
		t.Helper()
		r := call(t, srv.URL, "POST", "/api/ldap/login", map[string]string{"source": "ldap", "providerId": pid, "username": "alice", "password": "user-password"}, nil, secure)
		expect(t, r, 200)
		if r.cookie == nil || !r.cookie.Secure {
			t.Fatal("missing secure temporary cookie")
		}
		return r.cookie
	}
	normal := login()
	status := object(t, call(t, srv.URL, "GET", "/api/account/mfa", nil, normal, secure))
	if status["passwordRequired"] != true {
		t.Fatal("normal session lost proof requirement")
	}
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, normal, secure), 400)
	exec(`UPDATE users SET force_mfa=true WHERE id=$1`, uid)
	pending := login()
	assertCookieDuration(t, pending, 300)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending, secure), 401)
	status = object(t, call(t, srv.URL, "GET", "/api/account/mfa", nil, pending, secure))
	if status["passwordRequired"] != false || status["phase"] != "mfa_enroll" {
		t.Fatal(status)
	}
	bound := object(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, pending, secure))
	secret, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(bound["secret"].(string))
	if e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/confirm", map[string]string{"totp": "bad"}, pending, secure), 400)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending, secure), 401)
	confirmed := call(t, srv.URL, "POST", "/api/account/mfa/confirm", map[string]string{"totp": identity.TOTP(secret, time.Now().Unix()/30)}, pending, secure)
	expect(t, confirmed, 200)
	if confirmed.cookie == nil || !confirmed.cookie.Secure {
		t.Fatal("MFA upgrade lost HTTPS cookie")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, confirmed.cookie, secure), 200)
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, pending, secure), 401)
	// Provider revocation still invalidates the upgraded session.
	exec(`UPDATE ldap_identity_providers SET enabled=false WHERE id=$1`, pid)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, confirmed.cookie, secure), 401)
	exec(`UPDATE ldap_identity_providers SET enabled=true WHERE id=$1`, pid)
	exec(`DELETE FROM mfa_factors WHERE user_id=$1`, uid)
	exec(`UPDATE users SET force_mfa=true WHERE id=$1`, uid)
	expired := login()
	exec(`UPDATE auth_sessions SET absolute_expires_at=now()-interval '1 second' WHERE token_hash=$1`, identity.Digest(expired.Value))
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, expired, secure), 401)
	revoked := login()
	exec(`UPDATE ldap_identity_providers SET enabled=false WHERE id=$1`, pid)
	expect(t, call(t, srv.URL, "POST", "/api/account/mfa/enroll", map[string]string{}, revoked, secure), 401)
}
