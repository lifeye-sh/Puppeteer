package application

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/website"
	"strings"
	"testing"
	"time"
)

func TestR09LoginTemporaryIPAndWebSocketRevocation(t *testing.T) {
	for _, mode := range []string{"private", "temporary_ip"} {
		t.Run(mode, func(t *testing.T) {
			pool, a, srv := r02Server(t, false)
			admin := loginAs(t, srv, "accept-admin")
			uid := postID(t, srv, "/api/admin/users", userInput("website-operator", nil, nil), admin)
			user := loginAs(t, srv, "website-operator")
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ws" {
					c, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
					if e != nil {
						return
					}
					defer c.Close()
					for {
						kind, b, e := c.ReadMessage()
						if e != nil {
							return
						}
						if c.WriteMessage(kind, b) != nil {
							return
						}
					}
				}
				io.WriteString(w, "authorized")
			}))
			defer upstream.Close()
			domain := "login.example.test"
			cert := r09Certificate(t, a, domain)
			id := postID(t, srv, "/api/admin/websites", website.Website{Name: "login", Enabled: true, Domain: domain, EntryPath: "/", Upstream: upstream.URL, Mode: mode, CertificateID: cert, TempTTL: 5, TempMax: 10, TempAutoRenew: true}, admin)
			grant := postID(t, srv, "/api/admin/grants", map[string]string{"userId": uid, "resourceId": id}, admin)
			client, _ := r09Gateway(t, a, cert, nil)
			res, e := client.Get("https://" + domain + "/target?item=1")
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != 302 {
				t.Fatal(res.StatusCode)
			}
			location, e := url.Parse(res.Header.Get("Location"))
			if e != nil {
				t.Fatal(e)
			}
			state := location.Query().Get("state")
			var stateCookie *http.Cookie
			for _, c := range res.Cookies() {
				if c.Name == websiteStateCookie {
					stateCookie = c
				}
			}
			if stateCookie == nil || state == "" {
				t.Fatal("missing login state")
			}
			expect(t, call(t, srv.URL, "POST", "/api/portal/websites/"+id+"/login-code", map[string]string{"state": state, "return": "//evil.test"}, user, origin), 422)
			result := call(t, srv.URL, "POST", "/api/portal/websites/"+id+"/login-code", map[string]string{"state": state, "return": "/target?item=1"}, user, origin)
			expect(t, result, 200)
			callback := object(t, result)["url"].(string)
			req, _ := http.NewRequest("GET", callback, nil)
			wrong := *stateCookie
			wrong.Value = strings.Repeat("0", 64)
			req.AddCookie(&wrong)
			res, e = client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Fatal("state mismatch accepted")
			}
			req, _ = http.NewRequest("GET", callback, nil)
			req.AddCookie(stateCookie)
			res, e = client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != 303 || res.Header.Get("Location") != "/target?item=1" {
				t.Fatal("login callback", res.StatusCode)
			}
			var sessionCookie *http.Cookie
			for _, c := range res.Cookies() {
				if c.Name == websiteCookie {
					sessionCookie = c
				}
			}
			req, _ = http.NewRequest("GET", callback, nil)
			req.AddCookie(stateCookie)
			res, e = client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Fatal("code replay accepted")
			}
			if mode == "temporary_ip" {
				_, e = pool.Exec(context.Background(), "UPDATE website_temporary_ips SET expires_at=now()+interval '2 seconds',absolute_expires_at=now()+interval '30 seconds' WHERE website_id=$1", id)
				if e != nil {
					t.Fatal(e)
				}
			}
			req, _ = http.NewRequest("GET", "https://"+domain+"/", nil)
			if sessionCookie != nil {
				req.AddCookie(sessionCookie)
			}
			res, e = client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatal("authenticated access", res.StatusCode)
			}
			if mode == "temporary_ip" {
				var bounded bool
				e = pool.QueryRow(context.Background(), "SELECT expires_at=absolute_expires_at FROM website_temporary_ips WHERE website_id=$1", id).Scan(&bounded)
				if e != nil || !bounded {
					t.Fatal("renewal not capped", e)
				}
			}
			transport := client.Transport.(*http.Transport)
			dialer := websocket.Dialer{TLSClientConfig: transport.TLSClientConfig, NetDialContext: transport.DialContext}
			headers := http.Header{"Origin": []string{"https://" + domain}}
			if sessionCookie != nil {
				headers.Set("Cookie", sessionCookie.String())
			}
			ws, _, e := dialer.Dial("wss://"+domain+"/ws", headers)
			if e != nil {
				t.Fatal(e)
			}
			defer ws.Close()
			if e = ws.WriteMessage(websocket.TextMessage, []byte("echo")); e != nil {
				t.Fatal(e)
			}
			_, body, e := ws.ReadMessage()
			if e != nil || string(body) != "echo" {
				t.Fatal("websocket echo", e)
			}
			expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
			ws.SetReadDeadline(time.Now().Add(6 * time.Second))
			_, _, e = ws.ReadMessage()
			if e == nil {
				t.Fatal("socket not revoked")
			}
			if ne, ok := e.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatal("socket remained open until deadline")
			}
			req, _ = http.NewRequest("GET", "https://"+domain+"/", nil)
			if sessionCookie != nil {
				req.AddCookie(sessionCookie)
			}
			res, e = client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode == 200 {
				t.Fatal("revoked grant accepted")
			}
		})
	}
}

func TestR09PublicPasswordAndCertificateDefault(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	domain := "password.example.test"
	cert := r09Certificate(t, a, domain)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "protected") }))
	defer upstream.Close()
	input := map[string]any{"name": "password", "enabled": true, "domain": domain, "entryPath": "/", "upstream": upstream.URL, "mode": "public", "password": "internal-password-123", "certificateId": cert, "tempTtlMinutes": 5, "tempMaxMinutes": 10}
	postID(t, srv, "/api/admin/websites", input, admin)
	client, _ := r09Gateway(t, a, cert, nil)
	res, e := client.Get("https://" + domain + "/_nt/password")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	cookies := res.Cookies()
	var state *http.Cookie
	for _, c := range cookies {
		if c.Name == websiteStateCookie {
			state = c
		}
	}
	if state == nil {
		t.Fatal("missing password csrf")
	}
	req, _ := http.NewRequest("POST", "https://"+domain+"/_nt/password", strings.NewReader(url.Values{"password": {"internal-password-123"}, "state": {state.Value}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://"+domain)
	req.AddCookie(state)
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatal("password login", res.StatusCode)
	}
	req, _ = http.NewRequest("GET", "https://"+domain+"/", nil)
	for _, c := range res.Cookies() {
		req.AddCookie(c)
	}
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	settings := call(t, srv.URL, "GET", "/api/admin/website-settings", nil, admin, origin)
	expect(t, settings, 200)
	var in website.Settings
	if e = json.Unmarshal(settings.body, &in); e != nil {
		t.Fatal(e)
	}
	in.DefaultCertificateID = cert
	expect(t, call(t, srv.URL, "PUT", "/api/admin/website-settings", in, admin, origin), 204)
}
