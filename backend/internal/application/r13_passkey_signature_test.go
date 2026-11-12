package application

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/fxamacker/cbor/v2"
	"net/http/httptest"
	"testing"
)

func TestR13PasskeySignedLogin(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	changeLoginDuration(t, srv, admin, 7)
	uid := object(t, call(t, srv.URL, "GET", "/api/account/info", nil, admin, origin))["id"].(string)
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, "https://example.com")
	saveConfig := func(patch map[string]any, want int) {
		t.Helper()
		v, e := a.system.Configuration(context.Background(), 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": patch, "password": testPassword}, admin)
		expect(t, r, want)
	}
	saveConfig(map[string]any{"passkey-domain": "example.com", "passkey-origins": "[\"https://example.com\"]"}, 202)

	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	public, e := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte("isolated-test-authenticator-id")
	enc := base64.RawURLEncoding.EncodeToString
	reg := object(t, call(t, srv.URL, "POST", "/api/account/passkeys/start", map[string]string{"password": testPassword}, admin, "https://example.com"))
	regClient, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": reg["options"].(map[string]any)["challenge"], "origin": "https://example.com", "crossOrigin": false})
	rpHash := sha256.Sum256([]byte("example.com"))
	regAuth := append(append([]byte{}, rpHash[:]...), []byte{0x45, 0, 0, 0, 0}...)
	regAuth = append(regAuth, make([]byte, 16)...)
	regAuth = append(regAuth, byte(len(raw)>>8), byte(len(raw)))
	regAuth = append(regAuth, raw...)
	regAuth = append(regAuth, public...)
	attestation, e := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": regAuth})
	if e != nil {
		t.Fatal(e)
	}
	registration := map[string]any{"token": reg["token"], "name": "signed fixture", "response": map[string]any{"id": enc(raw), "rawId": enc(raw), "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": enc(regClient), "attestationObject": enc(attestation), "transports": []string{"internal"}}}}
	created := call(t, srv.URL, "POST", "/api/account/passkeys/finish", registration, admin, "https://example.com")
	expect(t, created, 201)
	id := object(t, created)["id"].(string)
	start := call(t, srv.URL, "POST", "/api/passkey/login/start", map[string]string{}, nil, "https://example.com")
	expect(t, start, 200)
	options := object(t, start)
	challenge := options["options"].(map[string]any)["challenge"].(string)
	client, e := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": "https://example.com", "crossOrigin": false})
	if e != nil {
		t.Fatal(e)
	}
	rp := sha256.Sum256([]byte("example.com"))
	auth := append(rp[:], []byte{5, 0, 0, 0, 1}...)
	clientHash := sha256.Sum256(client)
	signed := append(append([]byte{}, auth...), clientHash[:]...)
	hash := sha256.Sum256(signed)
	signature, e := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if e != nil {
		t.Fatal(e)
	}
	body := map[string]any{"token": options["token"], "response": map[string]any{"id": enc(raw), "rawId": enc(raw), "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": enc(client), "authenticatorData": enc(auth), "signature": enc(signature), "userHandle": enc([]byte(uid))}}}
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", "/api/passkey/login/finish", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://example.com")
	request.AddCookie(start.cookie)
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("Passkey finish status %d", response.Code)
	}
	found := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == CookieName {
			assertCookieDuration(t, cookie, 7*60)
			found = true
		}
	}
	if !found {
		t.Fatal("session cookie missing alongside challenge deletion")
	}
	expect(t, call(t, srv.URL, "POST", "/api/passkey/login/finish", body, start.cookie, "https://example.com"), 400)
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM passkey_credentials WHERE id=$1 AND last_used_at IS NOT NULL AND (credential->'authenticator'->>'signCount')::int=1`, id).Scan(&count); e != nil || count != 1 {
		t.Fatal("signature counter not persisted", e, count)
	}
	// A real signed login proves the sole alternative; it cannot be removed.
	saveConfig(map[string]any{"disable-password-login": true}, 202)
	saveConfig(map[string]any{"passkey-enabled": false}, 409)
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, "https://other.example.net", "https://login.example.com")
	saveConfig(map[string]any{"passkey-domain": "other.example.net", "passkey-origins": "[\"https://other.example.net\"]"}, 409)
	saveConfig(map[string]any{"disable-password-login": false}, 202)
	// Each rejection uses a fresh challenge and a cryptographically valid
	// signature except for the deliberately corrupted-signature case.
	for _, testCase := range []string{"signature", "origin", "user_handle", "user_verification", "counter", "configuration-disabled", "configuration-origin", "disabled", "revoked"} {
		t.Run(testCase, func(t *testing.T) {
			// Independent cases share a fixture, not the per-minute attempt budget.
			a.mu.Lock()
			a.attempts = map[string]attempt{}
			a.mu.Unlock()
			if testCase == "disabled" {
				if _, e := pool.Exec(context.Background(), `UPDATE users SET status='disabled' WHERE id=$1`, uid); e != nil {
					t.Fatal(e)
				}
				defer func() { _, _ = pool.Exec(context.Background(), `UPDATE users SET status='enabled' WHERE id=$1`, uid) }()
			}
			if testCase == "revoked" {
				expect(t, call(t, srv.URL, "DELETE", "/api/account/passkeys/"+id, map[string]string{"password": testPassword}, admin, origin), 204)
			}
			begin := call(t, srv.URL, "POST", "/api/passkey/login/start", map[string]string{}, nil, "https://example.com")
			expect(t, begin, 200)
			startData := object(t, begin)
			clientOrigin := "https://example.com"
			if testCase == "origin" {
				clientOrigin = "https://evil.example.com"
			}
			data, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": startData["options"].(map[string]any)["challenge"], "origin": clientOrigin, "crossOrigin": false})
			flags := byte(5)
			if testCase == "user_verification" {
				flags = 1
			}
			counter := byte(2)
			if testCase == "counter" {
				counter = 1
			}
			authData := append(append([]byte{}, rpHash[:]...), flags, 0, 0, 0, counter)
			clientDigest := sha256.Sum256(data)
			digest := sha256.Sum256(append(append([]byte{}, authData...), clientDigest[:]...))
			sig, e := ecdsa.SignASN1(rand.Reader, key, digest[:])
			if e != nil {
				t.Fatal(e)
			}
			if testCase == "signature" {
				sig[len(sig)-1] ^= 1
			}
			handle := uid
			if testCase == "user_handle" {
				handle = "wrong-user"
			}
			payload := map[string]any{"token": startData["token"], "response": map[string]any{"id": enc(raw), "rawId": enc(raw), "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": enc(data), "authenticatorData": enc(authData), "signature": enc(sig), "userHandle": enc([]byte(handle))}}}
			want := 400
			if testCase == "configuration-disabled" {
				saveConfig(map[string]any{"passkey-enabled": false}, 202)
				want = 503
			}
			if testCase == "configuration-origin" {
				saveConfig(map[string]any{"passkey-origins": "[\"https://login.example.com\"]"}, 202)
			}
			expect(t, call(t, srv.URL, "POST", "/api/passkey/login/finish", payload, begin.cookie, "https://example.com"), want)
			expect(t, call(t, srv.URL, "POST", "/api/passkey/login/finish", payload, begin.cookie, "https://example.com"), want)
			if testCase == "configuration-disabled" || testCase == "configuration-origin" {
				saveConfig(map[string]any{"passkey-enabled": true, "passkey-origins": "[\"https://example.com\"]"}, 202)
				expect(t, call(t, srv.URL, "POST", "/api/passkey/login/finish", payload, begin.cookie, "https://example.com"), 400)
			}

			var saved int
			if e := pool.QueryRow(context.Background(), `SELECT (credential->'authenticator'->>'signCount')::int FROM passkey_credentials WHERE id=$1`, id).Scan(&saved); e != nil || saved != 1 {
				t.Fatal("rejected login changed credential counter", e, saved)
			}
		})
	}
}
