package application

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestR06HTTPProxySSHAndDisable(t *testing.T) {
	host, port, _ := sshTarget(t)
	target := net.JoinHostPort(host, strconv.Itoa(port))
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != target {
			w.WriteHeader(403)
			return
		}
		username, password, ok := func() (string, string, bool) {
			clone := r.Clone(r.Context())
			clone.Header.Set("Authorization", r.Header.Get("Proxy-Authorization"))
			return clone.BasicAuth()
		}()
		if !ok || username != "tester" || password != "remote-only-secret-R03" {
			w.WriteHeader(407)
			return
		}
		remote, e := net.Dial("tcp", target)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		raw, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		calls.Add(1)
		_, _ = b.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = b.Flush()
		go func() {
			defer raw.Close()
			defer remote.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(remote, raw); _ = remote.(*net.TCPConn).CloseWrite(); close(done) }()
			_, _ = io.Copy(raw, remote)
			_ = raw.Close()
			_ = remote.Close()
			<-done
		}()
	}))
	defer proxy.Close()
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	id := r04Asset(t, srv, admin, host, port, true)
	r := call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	var asset resource.Asset
	_ = json.Unmarshal(r.body, &asset)
	u, _ := url.Parse(proxy.URL)
	p, _ := strconv.Atoi(u.Port())
	v := connectivity.Proxy{Name: "proxy", Scheme: "http", Host: u.Hostname(), Port: p, Enabled: true, CredentialID: asset.CredentialID}
	r = call(t, srv.URL, "POST", "/api/admin/network-proxies", v, admin, origin)
	expect(t, r, 200)
	v.ID = object(t, r)["id"].(string)
	v.Version = 1
	asset.ProxyID = v.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, asset, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 200)
	sid, ticket := reserve(t, srv, admin, id, false)
	ws := attach(t, srv, admin, sid, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+sid)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	if calls.Load() < 2 {
		t.Fatal("proxy bypass")
	}
	v.Enabled = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/network-proxies/"+v.ID, v, admin, origin), 200)
	waitClosed(t, a, sid)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
}
func TestR06GroupOrderingAndInvalidMembers(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	expect(t, call(t, srv.URL, "GET", "/api/admin/gateway-groups", nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/admin/gateway-groups", connectivity.GatewayGroup{Name: "empty", Enabled: true}, admin, origin), 422)
	expect(t, call(t, srv.URL, "POST", "/api/admin/gateway-groups", connectivity.GatewayGroup{Name: "nested", Enabled: true, Members: []connectivity.GatewayMember{{Kind: "group", ID: "00000000-0000-0000-0000-000000000000"}}}, admin, origin), 422)
}
