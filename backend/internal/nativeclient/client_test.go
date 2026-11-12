package nativeclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransportBoundary(t *testing.T) {
	for _, origin := range []string{"http://localhost:8089", "https://user:secret@example.test", "https://example.test/api", "https://example.test?token=x", "https://example.test#fragment"} {
		if _, e := New(origin, nil); e == nil {
			t.Fatal("unsafe origin accepted", origin)
		}
	}
	received := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c, e := New(redirect.URL, redirect.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Assets(context.Background(), Token{Access: "must-not-leak"}); e == nil {
		t.Fatal("redirect accepted")
	}
	if received {
		t.Fatal("bearer redirected to another host")
	}
	untrusted, e := New(redirect.URL, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = untrusted.Assets(context.Background(), Token{}); e == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}
