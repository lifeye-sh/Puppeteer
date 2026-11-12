package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"testing"
)

func TestR16OAuthKeyConfiguration(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, "https://native.test")
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(set map[string]any, clearKeys []string, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": set, "clearSecrets": clearKeys, "password": password}, admin)
		expect(t, r, want)
	}
	active := func(table string) string {
		t.Helper()
		var id string
		if e := pool.QueryRow(ctx, "SELECT id::text FROM "+table+" WHERE active").Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	// Run actual native grant validation before and after both independent rotations.
	token := nativeFixtureToken(t, a, admin, []string{"assets:read", "offline_access"})
	checkToken := func(token string, want bool) {
		t.Helper()
		err := a.db.Within(ctx, func(c context.Context) error {
			_, _, e := a.identity.OAuthToken(c, token, "access", identity.NativeClientUUID, false)
			return e
		})
		if (err == nil) != want {
			t.Fatalf("token validity: want %v, got %v", want, err)
		}
	}
	checkToken(token, true)
	original := head().ID
	oldSigning := active("oauth_signing_keys")
	oldMAC := active("oauth_hmac_keys")
	save(nil, []string{system.OAuthHMACKey}, "", 422)
	save(map[string]any{system.OAuthSigningKey: "invalid"}, nil, testPassword, 422)
	save(map[string]any{system.OAuthHMACKey: "not-base64"}, nil, testPassword, 422)
	for _, key := range [][]byte{a.config.EncryptionKey, a.config.VaultKey} {
		save(map[string]any{system.OAuthHMACKey: base64.RawURLEncoding.EncodeToString(key)}, nil, testPassword, 422)
	}
	if head().ID != original || active("oauth_hmac_keys") != oldMAC {
		t.Fatal("rejected update persisted")
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	mac := make([]byte, 32)
	if _, e = rand.Read(mac); e != nil {
		t.Fatal(e)
	}
	encoded := base64.RawURLEncoding.EncodeToString(mac)
	save(map[string]any{system.OAuthSigningKey: pemKey, system.OAuthHMACKey: encoded}, nil, testPassword, 202)
	e = a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{system.OAuthHMACKey: json.RawMessage(`""`)}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("missing service proof", e)
	}
	newSigning := active("oauth_signing_keys")
	newMAC := active("oauth_hmac_keys")
	if oldSigning == newSigning || oldMAC == newMAC {
		t.Fatal("keys did not rotate")
	}
	response := call(t, srv.URL, "GET", "/api/settings", nil, admin, origin)
	expect(t, response, 200)
	if strings.Contains(string(response.body), pemKey) || strings.Contains(string(response.body), encoded) || strings.Contains(string(response.body), head().Values.String(system.OAuthSigningKey)) {
		t.Fatal("secret leaked")
	}
	jwtValue, e := a.identity.SignOAuthIDToken(ctx, "https://native.test", identity.NativeClientUUID, "", map[string]any{"sub": "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := jwt.Parse(jwtValue, func(token *jwt.Token) (any, error) {
		if token.Header["kid"] != newSigning {
			t.Fatal("unexpected signing key")
		}
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}))
	if e != nil || !parsed.Valid {
		t.Fatal("import not used", e)
	}
	checkToken(token, true)
	// Masked and empty secret fields preserve the key, without secret readback.
	save(map[string]any{system.OAuthSigningKey: system.SecretMask, system.OAuthHMACKey: ""}, nil, testPassword, 202)
	if active("oauth_signing_keys") != newSigning || active("oauth_hmac_keys") != newMAC {
		t.Fatal("masked value rotated")
	}
	next := nativeFixtureToken(t, a, admin, []string{"assets:read", "offline_access"})
	checkToken(next, true)
	var id string
	var tag []byte
	if e = pool.QueryRow(ctx, `SELECT hmac_key_id::text,hmac_tag FROM oauth_tokens WHERE token_hash=$1`, identity.Digest(next)).Scan(&id, &tag); e != nil || id != newMAC || len(tag) != 32 {
		t.Fatal("new token missing HMAC", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE oauth_tokens SET hmac_tag=$2 WHERE token_hash=$1`, identity.Digest(next), make([]byte, 32)); e != nil {
		t.Fatal(e)
	}
	checkToken(next, false)
	if _, e = pool.Exec(ctx, `UPDATE oauth_tokens SET hmac_tag=$2 WHERE token_hash=$1`, identity.Digest(next), tag); e != nil {
		t.Fatal(e)
	}
	checkToken(next, true)
	// Restore compatibility for tokens issued before schema96, without changing their expiry.
	if _, e = pool.Exec(ctx, `UPDATE oauth_tokens SET hmac_key_id=NULL,hmac_tag=NULL WHERE token_hash=$1`, identity.Digest(token)); e != nil {
		t.Fatal(e)
	}
	checkToken(token, true)
	// Repeated auto-generation must create a revision even if the setting is already empty.
	for i := 0; i < 2; i++ {
		before := head().ID
		old := active("oauth_hmac_keys")
		save(nil, []string{system.OAuthSigningKey, system.OAuthHMACKey}, testPassword, 202)
		if head().ID <= before || active("oauth_hmac_keys") == old {
			t.Fatal("auto regeneration was ignored")
		}
		checkToken(next, true)
	}
	var jwk []byte
	if e = pool.QueryRow(ctx, `SELECT public_jwk FROM oauth_signing_keys WHERE active`).Scan(&jwk); e != nil {
		t.Fatal(e)
	}
	var pub map[string]string
	if e = json.Unmarshal(jwk, &pub); e != nil {
		t.Fatal(e)
	}
	n, e := base64.RawURLEncoding.DecodeString(pub["n"])
	if e != nil || len(n) != 384 {
		t.Fatal("automatic RSA not 3072 bits")
	}
	var retained bool
	if e = pool.QueryRow(ctx, `SELECT NOT active AND retire_at>now()+interval '60 minutes' FROM oauth_signing_keys WHERE id=$1`, newSigning).Scan(&retained); e != nil || !retained {
		t.Fatal("old JWT key retired early", e)
	}
	if e = a.db.Within(ctx, a.identity.QuarantineRestore); e != nil {
		t.Fatal(e)
	}
	if head().Values.String(system.OAuthSigningKey) != "" || head().Values.String(system.OAuthHMACKey) != "" {
		t.Fatal("restored custom key still configured")
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM oauth_signing_keys WHERE active)+(SELECT count(*) FROM oauth_hmac_keys WHERE active)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("restore active keys", e)
	}
	checkToken(next, false)
}

func TestR16OAuthKeysRequireFreshMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"clearSecrets": []string{system.OAuthHMACKey}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
