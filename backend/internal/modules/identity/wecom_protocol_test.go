package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestWeComProtocol(t *testing.T) {
	mode := "ok"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "redirect" {
			http.Redirect(w, r, "https://example.com", 302)
			return
		}
		if r.URL.Path == "/cgi-bin/gettoken" {
			if r.URL.Query().Get("corpid") != "corp" || r.URL.Query().Get("corpsecret") != "secret" {
				t.Error("wrong client proof")
			}
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "token", "expires_in": 7200})
			return
		}
		if r.URL.Path != "/cgi-bin/auth/getuserinfo" || r.URL.Query().Get("access_token") != "token" || r.URL.Query().Get("code") != "once" {
			t.Error("wrong identity exchange")
		}
		switch mode {
		case "external":
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "openid": "external"})
		case "error":
			json.NewEncoder(w).Encode(map[string]any{"errcode": 40029, "userid": "member"})
		case "missing-code":
			json.NewEncoder(w).Encode(map[string]any{"userid": "member"})
		default:
			json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "member"})
		}
	}))
	defer srv.Close()
	p := OIDCProvider{Kind: "wecom", Name: "fixture", Issuer: srv.URL, ClientID: "corp", AgentID: "1001", AuthorizationURL: srv.URL + "/authorize", RedirectURI: "https://backend.localhost/api/wechat-work/callback"}
	protocol, e := NewWeComProtocol(p, "secret", srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	raw, c := protocol.Begin()
	u, _ := url.Parse(raw)
	if u.Query().Get("appid") != "corp" || u.Query().Get("state") != c.State || u.Query().Get("login_type") != "CorpApp" {
		t.Fatal("wrong authorization")
	}
	q := url.Values{"code": {"once"}, "state": {c.State}}
	subject, e := protocol.Exchange(context.Background(), c, q)
	if e != nil || subject.Subject != "corp:member" || subject.Issuer != srv.URL {
		t.Fatal("identity", subject, e)
	}
	for _, m := range []string{"external", "error", "missing-code", "redirect"} {
		mode = m
		if _, e = protocol.Exchange(context.Background(), c, q); e == nil {
			t.Fatal("accepted", m)
		}
	}
	mode = "ok"
	q.Set("state", "wrong")
	if _, e = protocol.Exchange(context.Background(), c, q); e == nil {
		t.Fatal("state")
	}
	q.Set("state", c.State)
	q.Add("code", "twice")
	if _, e = protocol.Exchange(context.Background(), c, q); e == nil {
		t.Fatal("duplicate code")
	}
	q.Set("code", "once")
	c.ExpiresAt = time.Now().Add(-time.Second)
	if _, e = protocol.Exchange(context.Background(), c, q); e == nil {
		t.Fatal("expired")
	}
	_, c = protocol.Begin()
	untrusted, _ := NewWeComProtocol(p, "secret", nil)
	if _, e = untrusted.Exchange(context.Background(), c, url.Values{"code": {"once"}, "state": {c.State}}); e == nil {
		t.Fatal("untrusted TLS")
	}
}
