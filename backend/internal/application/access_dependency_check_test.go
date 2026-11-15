package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/configstate"
)

func TestAccessDependencyKeysRevocationAndEvidence(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	var issuer string
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("preflight wrote to issuer")
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(identity.OIDCDiscovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/api/oidc/server/jwks", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}})
		case "/api/oidc/server/jwks":
			keys, e := a.identity.OAuthJWKS(ctx)
			if e != nil {
				t.Error(e)
			}
			json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer vendor.Close()
	issuer = vendor.URL
	a.oidcHTTPClient = vendor.Client()
	probe := func(feature, want string) configstate.Task {
		t.Helper()
		head, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", configTag(head.ID), map[string]any{}, admin)
		expect(t, r, 202)
		task := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
		if task.State != want {
			t.Fatal(task)
		}
		return task
	}
	probe("oauthServer", "failed")
	if _, e := pool.Exec(ctx, `UPDATE oauth_server_settings SET issuer=$1,enabled=true,version=version+1`, issuer); e != nil {
		t.Fatal(e)
	}
	if e := a.identity.EnsureOAuthKey(ctx, false); e != nil {
		t.Fatal(e)
	}
	if e := a.identity.EnsureOAuthHMACKey(ctx, false); e != nil {
		t.Fatal(e)
	}
	first := probe("oauthServer", "succeeded")
	probe("native-client", "failed")     // No protocol listener or client permission is implied by an issuer.
	probe("forward", "succeeded")        // Disabled and no live streams.
	probe("databasePolicy", "succeeded") // Disabled and no live DML sessions.
	if e := a.identity.EnsureOAuthKey(ctx, true); e != nil {
		t.Fatal(e)
	}
	head, _ := a.system.Configuration(ctx, 0)
	next, _, e := a.dependencyEvidence(ctx, head.Values, "oauthServer", first.Actor)
	if e != nil || next == first.Evidence {
		t.Fatal("key rotation retained proof", e)
	}
	var before, after int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM oauth_signing_keys`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE oauth_signing_keys SET public_jwk=jsonb_set(public_jwk,'{n}','"invalid"') WHERE active`); e != nil {
		t.Fatal(e)
	}
	probe("oauthServer", "failed")
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM oauth_signing_keys`).Scan(&after); e != nil || before != after {
		t.Fatal("preflight regenerated keys", e)
	}
}
