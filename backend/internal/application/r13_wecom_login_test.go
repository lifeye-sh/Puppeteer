package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"strings"
	"sync/atomic"
	"testing"
)

func TestR13WeComBindingAndLogin(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	const secure = "https://backend.localhost:5174"
	a.config.AllowedOrigins = append(a.config.AllowedOrigins, secure)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("wecom-owner", nil, nil), admin)
	owner := loginAs(t, srv, "wecom-owner")
	postID(t, srv, "/api/admin/users", userInput("wecom-other", nil, nil), admin)
	other := loginAs(t, srv, "wecom-other")
	var nonmember atomic.Bool
	var exchanges atomic.Int64
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpid") != "ww-fixture" || r.URL.Query().Get("corpsecret") != "fixture-secret" {
				t.Error("wrong WeCom application credentials")
			}
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "vendor-token", "expires_in": 7200})
		case "/cgi-bin/auth/getuserinfo":
			exchanges.Add(1)
			if r.URL.Query().Get("access_token") != "vendor-token" || r.URL.Query().Get("code") != "fixture-code" {
				t.Error("wrong WeCom exchange")
			}
			if nonmember.Load() {
				json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "openid": "visitor"})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "member-1"})
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer vendor.Close()
	a.oidcHTTPClient = vendor.Client()
	provider := map[string]any{"name": "WeCom isolated fixture", "kind": "wecom", "issuer": vendor.URL, "authorizationUrl": vendor.URL + "/login", "agentId": "100001", "clientId": "ww-fixture", "clientSecret": "fixture-secret", "redirectUri": secure + "/api/wechat-work/callback", "enabled": true, "bindingEnabled": true, "password": testPassword}
	pid := postID(t, srv, "/api/admin/oidc-providers", provider, admin)
	proof := map[string]string{"password": testPassword}
	expect(t, call(t, srv.URL, "POST", "/api/admin/oidc-providers/"+pid+"/test", nil, admin, origin), 200)
	authURL := func(r result) string {
		expect(t, r, 200)
		u, e := url.Parse(object(t, r)["authorizationUrl"].(string))
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		if q.Get("appid") != "ww-fixture" || q.Get("agentid") != "100001" || q.Get("redirect_uri") != secure+"/api/wechat-work/callback" {
			t.Fatal("WeCom authorization parameters")
		}
		return "/api/wechat-work/callback?" + url.Values{"state": {q.Get("state")}, "code": {"fixture-code"}}.Encode()
	}
	loginStart := func() (string, *http.Cookie) {
		a.mu.Lock()
		a.attempts = map[string]attempt{}
		a.mu.Unlock()
		r := call(t, srv.URL, "POST", "/api/oidc/login/"+pid+"/start", map[string]string{}, nil, secure)
		return authURL(r), r.cookie
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	finish := func(path string, c *http.Cookie) *http.Cookie {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if c != nil {
			req.AddCookie(c)
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		for _, c := range res.Cookies() {
			if c.Name == CookieName && c.Value != "" {
				if !c.Secure || !c.HttpOnly {
					t.Fatal("unsafe federation cookie")
				}
				return c
			}
		}
		return nil
	}
	cb, cookie := loginStart()
	if finish(cb, cookie) != nil {
		t.Fatal("unbound user auto-provisioned")
	}
	bindStart := func(c *http.Cookie) string {
		return authURL(call(t, srv.URL, "POST", "/api/account/oidc-providers/"+pid+"/bind", proof, c, secure))
	}
	cb = bindStart(owner)
	expect(t, call(t, srv.URL, "GET", cb, nil, other, origin), 400)
	expect(t, call(t, srv.URL, "GET", cb, nil, owner, origin), 200)
	expect(t, call(t, srv.URL, "GET", cb, nil, owner, origin), 400)
	var eid, subject string
	if e := pool.QueryRow(ctx, `SELECT id,subject FROM external_identities WHERE user_id=$1`, uid).Scan(&eid, &subject); e != nil || subject != "ww-fixture:member-1" {
		t.Fatal("tenant-scoped subject", e)
	}
	cb = bindStart(other)
	expect(t, call(t, srv.URL, "GET", cb, nil, other, origin), 409)
	cb, cookie = loginStart()
	wrong := *cookie
	wrong.Value = identity.NewToken()
	if finish(cb, &wrong) != nil {
		t.Fatal("cross-browser login")
	}
	logged := finish(cb, cookie)
	if logged == nil {
		t.Fatal("WeCom login failed")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, origin), 200)
	if finish(cb, cookie) != nil {
		t.Fatal("callback replay")
	}
	cb, cookie = loginStart()
	before := exchanges.Load()
	if finish(strings.Replace(cb, "/api/wechat-work/callback", "/api/oidc/callback", 1), cookie) != nil || exchanges.Load() != before {
		t.Fatal("provider callback mixup")
	}
	nonmember.Store(true)
	cb, cookie = loginStart()
	if finish(cb, cookie) != nil {
		t.Fatal("visitor account accepted")
	}
	nonmember.Store(false)
	provider["version"] = 1
	provider["enabled"] = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+pid, provider, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, origin), 401)
	provider["version"] = 2
	provider["enabled"] = true
	expect(t, call(t, srv.URL, "PUT", "/api/admin/oidc-providers/"+pid, provider, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, logged, origin), 401)
	_, _, owner = enroll(t, srv, owner)
	cb, cookie = loginStart()
	pending := finish(cb, cookie)
	if pending == nil {
		t.Fatal("MFA phase absent")
	}
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, pending, origin), 401)
	status := object(t, call(t, srv.URL, "GET", "/api/login-status", nil, pending, origin))
	if status["phase"] != "mfa_pending" {
		t.Fatal("WeCom bypassed MFA")
	}
	// Reset solely inside the isolated fixture, then revoke the binding through its real HTTP endpoint.
	pool.Exec(ctx, `DELETE FROM mfa_factors WHERE user_id=$1`, uid)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/oidc-identities/"+eid, proof, other, origin), 403)
	expect(t, call(t, srv.URL, "DELETE", "/api/account/oidc-identities/"+eid, proof, owner, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/login-status", nil, pending, origin), 200)
	cb, cookie = loginStart()
	if finish(cb, cookie) != nil {
		t.Fatal("unbound WeCom identity logged in")
	}
}
