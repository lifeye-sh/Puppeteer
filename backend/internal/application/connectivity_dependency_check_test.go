package application

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"puppeteer/backend/internal/modules/connectivity"
	"puppeteer/backend/internal/modules/website"
	"puppeteer/backend/internal/platform/deploytask"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConnectivityDependencyUsesActualProxyAndLiveProcess(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	var actor string
	if e := pool.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	var business, connects atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { business.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != u.Host {
			w.WriteHeader(403)
			return
		}
		remote, e := net.Dial("tcp", u.Host)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		client, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		connects.Add(1)
		b.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		b.Flush()
		go func() {
			defer client.Close()
			defer remote.Close()
			done := make(chan struct{})
			go func() { io.Copy(remote, client); remote.(*net.TCPConn).CloseWrite(); close(done) }()
			io.Copy(client, remote)
			client.Close()
			remote.Close()
			<-done
		}()
	}))
	defer proxy.Close()
	p, _ := url.Parse(proxy.URL)
	port, _ := strconv.Atoi(p.Port())
	proxyID := postID(t, srv, "/api/admin/network-proxies", connectivity.Proxy{Name: "preflight actual proxy", Scheme: "http", Host: p.Hostname(), Port: port, Enabled: true}, admin)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.TLS.Certificates[0].Certificate[0]}))
	siteID := postID(t, srv, "/api/admin/websites", website.Website{Name: "proxy preflight site", Domain: "preflight.example.test", EntryPath: "/", Upstream: target.URL, UpstreamCA: ca, ProxyID: proxyID, Mode: "public", TempTTL: 30, TempMax: 120, CertificateID: r09Certificate(t, a, "preflight.example.test")}, admin)
	feature := "network-proxy:" + proxyID
	probe := func(want string) {
		t.Helper()
		snapshot := object(t, call(t, srv.URL, "GET", "/api/settings/operations/"+feature, nil, admin, origin))
		head, _ := a.system.Configuration(ctx, 0)
		res, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/"+feature+"/preflight", configTag(head.ID), map[string]any{"evidence": snapshot["evidence"]}, admin)
		expect(t, res, 202)
		task := configurationTaskResult(t, srv, admin, object(t, res)["id"].(string))
		if task.State != want {
			t.Fatal(task)
		}
	}
	probe("succeeded")
	if connects.Load() != 1 || business.Load() != 0 {
		t.Fatal("probe bypassed proxy or sent business HTTP", connects.Load(), business.Load())
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	secret := strings.Repeat("a", 64)
	os.WriteFile(tokenFile, []byte(secret), 0600)
	a.config.DeployTokenFile = tokenFile
	site, e := a.websites().Get(ctx, siteID)
	if e != nil {
		t.Fatal(e)
	}
	request := deploytask.Request{Protocol: "website", Enabled: true, Bind: "127.0.0.1", Port: 18443, SiteID: siteID, SiteVersion: site.Version, SiteEnabled: true, Actor: actor}
	raw, _ := json.Marshal(request)
	internal := func(token, originHeader string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/api/internal/deployment-check", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if originHeader != "" {
			req.Header.Set("Origin", originHeader)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode == 200 {
			var out struct{ Passed bool }
			json.NewDecoder(res.Body).Decode(&out)
			if !out.Passed {
				t.Fatal("live path preflight failed")
			}
		}
		return res.StatusCode
	}
	if internal(secret, "") != 200 || internal("wrong", "") != 403 || internal(secret, origin) != 403 {
		t.Fatal("internal endpoint authentication failed")
	}
	proxy.Close()
	probe("failed")
	if business.Load() != 0 {
		t.Fatal("failed proxy fell back to direct")
	}
	if e = a.DeploymentApplicationCheck(ctx, request); e == nil {
		t.Fatal("broken route enabled website")
	}
	site, _ = a.websites().Get(ctx, siteID)
	if site.Enabled {
		t.Fatal("preflight changed website")
	}
}
