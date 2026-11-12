package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/website"
	"strings"
	"testing"
)

func r09Certificate(t *testing.T, a *App, domain string) string {
	t.Helper()
	cert, key, e := pki.Generate("test server", []string{domain}, 2, false, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(key)
	var id string
	e = a.db.Within(context.Background(), func(ctx context.Context) error {
		var e error
		id, e = a.certificates().Store(ctx, pki.Certificate{Name: "test server", Kind: "server", Source: "self_signed", PEM: string(cert)}, key)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func r09Gateway(t *testing.T, a *App, certID string, clientCert *tls.Certificate) (*http.Client, *httptest.Server) {
	t.Helper()
	gateway := httptest.NewUnstartedServer(a.WebsiteHandler())
	gateway.TLS = a.WebsiteTLSConfig()
	gateway.StartTLS()
	t.Cleanup(gateway.Close)
	cert, e := a.certificates().Get(context.Background(), certID)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(cert.PEM))
	config := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	if clientCert != nil {
		config.Certificates = []tls.Certificate{*clientCert}
	}
	transport := &http.Transport{TLSClientConfig: config, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", gateway.Listener.Addr().String())
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, gateway
}
func TestR09HTTPSProxyIsolationRewriteAndDomain(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	domain := "service.example.test"
	cert := r09Certificate(t, a, domain)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), "nt_session") || strings.Contains(r.Header.Get("Cookie"), "__Host-nt-") {
			t.Error("platform identity leaked")
		}
		if r.Header.Get("X-Forwarded-For") == "198.51.100.7" {
			t.Error("forged XFF accepted")
		}
		if r.Header.Get("X-Test") != "configured" {
			t.Error("configured header missing")
		}
		w.Header().Set("Content-Type", "text/html")
		http.SetCookie(w, &http.Cookie{Name: "__Host-nt-website", Value: "malicious"})
		http.SetCookie(w, &http.Cookie{Name: "app_session", Value: "upstream", Domain: "bad.example"})
		_, _ = io.WriteString(w, "<h1>OLD</h1>")
	}))
	defer upstream.Close()
	input := website.Website{Name: "service", Enabled: true, Domain: domain, EntryPath: "/", Upstream: upstream.URL, Mode: "public", CertificateID: cert, TempTTL: 30, TempMax: 120, Audit: true, RequestHeaders: map[string]string{"X-Test": "configured"}, Rewrites: []website.Rewrite{{Find: "OLD", Replace: "NEW"}}}
	postID(t, srv, "/api/admin/websites", input, admin)
	client, _ := r09Gateway(t, a, cert, nil)
	req, _ := http.NewRequest("GET", "https://"+domain+"/", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	req.Header.Set("Authorization", "Bearer console-secret")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "private"})
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "<h1>NEW</h1>" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	for _, cookie := range res.Cookies() {
		if reservedCookie(cookie.Name) || cookie.Domain != "" || !cookie.Secure {
			t.Fatal("unsafe cookie", cookie)
		}
	}
	req, _ = http.NewRequest("GET", "https://"+domain+"/", nil)
	req.Host = "forged.example.test"
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 421 {
		t.Fatal(res.StatusCode)
	}
	input.Name = "duplicate"
	input.EntryPath = "/different"
	expect(t, call(t, srv.URL, "POST", "/api/admin/websites", input, admin, origin), 409)
	input.Domain = "wrong.example.test"
	expect(t, call(t, srv.URL, "POST", "/api/admin/websites", input, admin, origin), 422)
}
func TestR09ClientCertificateRequiresGrantAndRevocation(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	domain := "mtls.example.test"
	serverCert := r09Certificate(t, a, domain)
	user := postID(t, srv, "/api/admin/users", userInput("mtls-client", nil, nil), admin)
	ca := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "client CA", "kind": "client_ca", "source": "self_signed", "days": 2}, admin)
	clientID := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "operator certificate", "kind": "client", "parentId": ca, "userId": user, "days": 1}, admin)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "private") }))
	defer upstream.Close()
	id := postID(t, srv, "/api/admin/websites", website.Website{Name: "mTLS", Enabled: true, Domain: domain, EntryPath: "/", Upstream: upstream.URL, Mode: "private", CertificateID: serverCert, ClientCAID: ca, TempTTL: 30, TempMax: 60}, admin)
	cert, e := a.certificates().TLS(ctx, clientID)
	if e != nil {
		t.Fatal(e)
	}
	client, _ := r09Gateway(t, a, serverCert, &cert)
	res, e := client.Get("https://" + domain)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("certificate bypassed grant: %d", res.StatusCode)
	}
	postID(t, srv, "/api/admin/grants", map[string]string{"userId": user, "resourceId": id}, admin)
	res, e = client.Get("https://" + domain)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/certificates/"+clientID+"/revoke", map[string]int{"version": 1}, admin, origin), 204)
	res, e = client.Get("https://" + domain)
	if e == nil {
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("revoked certificate accepted")
		}
	}
}
