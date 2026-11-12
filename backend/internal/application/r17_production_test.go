package application

import (
	"net/http"
	"testing"
)

func TestR17ProductionCookies(t *testing.T) {
	_, a, srv := r02Server(t, false)
	a.config.SecureCookies = true
	challenge := call(t, srv.URL, "GET", "/api/captcha", nil, nil, origin)
	expect(t, challenge, 200)
	if challenge.cookie == nil || !challenge.cookie.Secure {
		t.Fatal("production captcha cookie is not Secure")
	}
	session := loginAs(t, srv, "accept-admin")
	if !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatal("production session cookie attributes incorrect")
	}
	logout := call(t, srv.URL, "POST", "/api/logout", nil, session, origin)
	expect(t, logout, 204)
	if logout.cookie == nil || !logout.cookie.Secure || logout.cookie.MaxAge != -1 {
		t.Fatal("logout must expire a Secure cookie")
	}
}
