package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOIDCDiscoveryTLSAndValidation(t *testing.T) {
	for _, mode := range []string{"valid", "issuer", "http_endpoint", "algorithm", "pkce", "redirect", "oversized", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/tenant/.well-known/openid-configuration" {
					t.Errorf("wrong discovery path %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("credential sent")
				}
				if mode == "redirect" {
					http.Redirect(w, r, "https://example.com/", 302)
					return
				}
				if mode == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat("x", 256*1024+1)))
					return
				}
				if mode == "malformed" {
					_, _ = w.Write([]byte("{"))
					return
				}
				v := OIDCDiscovery{Issuer: srv.URL + "/tenant", AuthorizationEndpoint: srv.URL + "/authorize", TokenEndpoint: srv.URL + "/token", JWKSURI: srv.URL + "/keys", ResponseTypes: []string{"code"}, SigningAlgorithms: []string{"RS256"}, PKCEMethods: []string{"S256"}}
				switch mode {
				case "issuer":
					v.Issuer += "/"
				case "http_endpoint":
					v.TokenEndpoint = "http://example.com/token"
				case "algorithm":
					v.SigningAlgorithms = []string{"none"}
				case "pkce":
					v.PKCEMethods = []string{"plain"}
				}
				_ = json.NewEncoder(w).Encode(v)
			}))
			defer srv.Close()
			_, e := DiscoverOIDC(context.Background(), srv.URL+"/tenant", srv.Client())
			if (e == nil) != (mode == "valid") {
				t.Fatalf("unexpected result %v", e)
			}
			if mode == "valid" {
				if _, e = DiscoverOIDC(context.Background(), srv.URL+"/tenant", nil); e == nil {
					t.Fatal("untrusted TLS accepted")
				}
			}
		})
	}
}
