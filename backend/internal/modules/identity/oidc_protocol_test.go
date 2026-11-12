package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

func TestOIDCAuthorizationProtocol(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, mode string
	var challenge OIDCAuthorization
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tenant/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(OIDCDiscovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/keys", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}})
		case "/tenant/keys":
			if r.Header.Get("Authorization") != "" {
				t.Error("credentials leaked to JWKS")
			}
			if mode == "huge-jwks" {
				w.Write([]byte(strings.Repeat("x", 256*1024+1)))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "fixture", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/tenant/token":
			requests++
			user, password, ok := r.BasicAuth()
			if !ok || user != "nt" || password != "test-secret" || r.Method != "POST" {
				t.Error("invalid client authentication")
			}
			r.ParseForm()
			if r.Form.Get("code_verifier") != challenge.Verifier || r.Form.Get("code") != "test-code" || r.Form.Get("redirect_uri") != "https://backend.example/api/oidc/callback" || r.Form.Get("grant_type") != "authorization_code" {
				t.Error("invalid code exchange")
			}
			if mode == "redirect" {
				http.Redirect(w, r, issuer+"/stolen", 307)
				return
			}
			if mode == "token-error" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"secret must not leak"}`))
				return
			}
			claims := jwt.MapClaims{"iss": issuer, "sub": "external-123", "aud": "nt", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": challenge.Nonce}
			signer := key
			switch mode {
			case "issuer":
				claims["iss"] = issuer + "/other"
			case "audience":
				claims["aud"] = "other-client"
			case "expiry":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "nonce":
				claims["nonce"] = "wrong"
			case "subject":
				claims["sub"] = ""
			case "future":
				claims["iat"] = time.Now().Add(time.Hour).Unix()
			case "azp":
				claims["azp"] = "other-client"
			case "multiple-aud":
				claims["aud"] = []string{"nt", "other-client"}
			case "hash":
				claims["at_hash"] = "wrong"
			case "signature":
				signer = wrongKey
			}
			value := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			value.Header["kid"] = "fixture"
			raw, e := value.SignedString(signer)
			if e != nil {
				t.Error(e)
			}
			if mode == "unsigned" {
				raw, _ = jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": raw})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	issuer = srv.URL + "/tenant"
	p, err := NewOIDCProtocol(context.Background(), OIDCProvider{Name: "test", Issuer: issuer, ClientID: "nt", RedirectURI: "https://backend.example/api/oidc/callback"}, "test-secret", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "issuer", "audience", "expiry", "nonce", "subject", "future", "azp", "multiple-aud", "signature", "unsigned", "hash", "redirect", "token-error", "huge-jwks", "state", "duplicate", "expired-challenge", "provider-error", "response-issuer"} {
		t.Run(scenario, func(t *testing.T) {
			mode = scenario
			var authorization string
			authorization, challenge = p.Begin()
			u, _ := url.Parse(authorization)
			q := u.Query()
			if q.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(challenge.Verifier) || q.Get("code_challenge_method") != "S256" || q.Get("nonce") != challenge.Nonce || q.Get("state") != challenge.State || q.Get("scope") != "openid" || q.Get("response_type") != "code" {
				t.Fatal("invalid authorization request")
			}
			query := url.Values{"state": {challenge.State}, "code": {"test-code"}}
			switch mode {
			case "state":
				query.Set("state", "wrong")
			case "duplicate":
				query.Add("code", "second")
			case "expired-challenge":
				challenge.ExpiresAt = time.Now().Add(-time.Second)
			case "provider-error":
				query.Set("error", "access_denied")
			case "response-issuer":
				query.Set("iss", "https://wrong.example")
			}
			before := requests
			subject, e := p.Exchange(context.Background(), challenge, query)
			if mode == "valid" {
				if e != nil || subject.Issuer != issuer || subject.Subject != "external-123" {
					t.Fatal(subject, e)
				}
			} else if e != ErrOIDCAuthorization || subject != (OIDCSubject{}) {
				t.Fatal("invalid response accepted", subject, e)
			}
			if (mode == "state" || mode == "duplicate" || mode == "expired-challenge" || mode == "provider-error" || mode == "response-issuer") && requests != before {
				t.Fatal("invalid callback contacted token endpoint")
			}
		})
	}
}
