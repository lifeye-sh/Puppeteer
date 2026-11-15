package application

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/modules/website"
	"puppeteer/backend/internal/platform/deploytask"
	"testing"
	"time"
)

func TestDeploymentCertificateChecksIdentityAndTime(t *testing.T) {
	now := time.Now()
	leaf := &x509.Certificate{DNSNames: []string{"db.example.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	if err := deploymentCertificate(leaf, "db.example.com", now); err != nil {
		t.Fatal(err)
	}
	if deploymentCertificate(leaf, "other.example.com", now) == nil {
		t.Fatal("wrong identity accepted")
	}
	if deploymentCertificate(leaf, "db.example.com", now.Add(2*time.Hour)) == nil {
		t.Fatal("expired certificate accepted")
	}
	if deploymentCertificate(leaf, "db.example.com", now.Add(-2*time.Hour)) == nil {
		t.Fatal("future certificate accepted")
	}
	if deploymentCertificate(nil, "db.example.com", now) == nil {
		t.Fatal("missing certificate accepted")
	}
}

func TestDeploymentDisableOverridesPersistedListener(t *testing.T) {
	a := &App{config: Config{DisabledDeploymentProtocols: map[string]bool{"mysql": true, "postgres": true, "ssh": true, "website": true}}}
	values := system.ConfigurationValues{"db-proxy-enabled": true, "db-proxy-addr": "0.0.0.0:13306", "postgres-proxy-enabled": true, "postgres-proxy-addr": "0.0.0.0:15432", "ssh-server-enabled": true, "ssh-server-addr": "0.0.0.0:2022", "website-server-enabled": true, "website-server-addr": "0.0.0.0:18443"}
	for _, name := range []string{"mysql", "postgres", "ssh"} {
		if address := a.requestedListenerAddress(values, name); address != "" {
			t.Fatal(name, address)
		}
	}
	for _, spec := range a.publicListenerSpecs(values) {
		if spec.Name == "website" {
			t.Fatal("disabled website listener created")
		}
	}
}

func TestFirstDatabaseDeploymentPreflightDoesNotPersistCertificate(t *testing.T) {
	pool, a, _ := r02Server(t, false)
	a.config.DatabaseProxyName = "db.example.com"
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM settings WHERE key='database-proxy-certificate-id'`); err != nil {
		t.Fatal(err)
	}
	if err := a.DeploymentApplicationCheck(ctx, deploytask.Request{Protocol: "mysql", Enabled: true, Bind: "127.0.0.1", Port: 13306}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM settings WHERE key='database-proxy-certificate-id'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestWebsiteDeploymentEnableDisableAndVersion(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer upstream.Close()
	cert := r09Certificate(t, a, "site.example.test")
	id := postID(t, srv, "/api/admin/websites", website.Website{Name: "site", Enabled: false, Domain: "site.example.test", EntryPath: "/", Upstream: upstream.URL, Mode: "public", CertificateID: cert, TempTTL: 30, TempMax: 120}, admin)
	var actor string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE username='accept-admin'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	site, err := a.websites().Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	request := deploytask.Request{Protocol: "website", Enabled: true, Bind: "127.0.0.1", Port: 18443, SiteID: id, SiteVersion: site.Version, SiteEnabled: true, Actor: actor}
	if err = a.DeploymentApplicationCheck(ctx, request); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := a.websites().Get(ctx, id)
	if unchanged.Enabled {
		t.Fatal("preflight enabled website")
	}
	if err = a.ApplyDeploymentWebsite(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err = a.ApplyDeploymentWebsite(ctx, request); err != nil {
		t.Fatal("committed enable retry failed", err)
	}
	cert2 := r09Certificate(t, a, "second.example.test")
	secondID := postID(t, srv, "/api/admin/websites", website.Website{Name: "second", Enabled: true, Domain: "second.example.test", EntryPath: "/", Upstream: upstream.URL, Mode: "public", CertificateID: cert2, TempTTL: 30, TempMax: 120}, admin)
	current, _ := a.websites().Get(ctx, id)
	request.SiteVersion = current.Version
	request.SiteEnabled = false
	request.Enabled = true
	if err = a.ApplyDeploymentWebsite(ctx, request); err != nil {
		t.Fatal(err)
	}
	current, _ = a.websites().Get(ctx, id)
	if current.Enabled {
		t.Fatal("website not disabled")
	}
	if err = a.ApplyDeploymentWebsite(ctx, request); err != nil {
		t.Fatal("disable retry failed", err)
	}
	second, err := a.websites().Get(ctx, secondID)
	if err != nil || !second.Enabled {
		t.Fatal("other website affected", err)
	}
	request.SiteID = secondID
	request.SiteVersion = second.Version
	request.Enabled = false
	if err = a.ApplyDeploymentWebsite(ctx, request); err != nil {
		t.Fatal("last website disable failed", err)
	}

}
