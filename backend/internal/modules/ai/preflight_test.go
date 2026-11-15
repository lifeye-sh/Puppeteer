package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModelPreflightDoesNotCallInference(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	p := Profile{BaseURL: srv.URL, AllowPrivate: true, APIKey: "not-sent"}
	if err := ProbeEndpoint(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("preflight made an HTTP request")
	}
	p.AllowPrivate = false
	if ProbeEndpoint(context.Background(), p) == nil {
		t.Fatal("private HTTP allowed without opt-in")
	}
	p.AllowPrivate = true
	p.ProxyURL = "http://127.0.0.1:1"
	if ProbeEndpoint(context.Background(), p) == nil {
		t.Fatal("unavailable proxy bypassed")
	}
	secure := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer secure.Close()
	p.BaseURL = secure.URL
	p.ProxyURL = ""
	if ProbeEndpoint(context.Background(), p) == nil {
		t.Fatal("untrusted certificate accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("credential or business request sent")
	}
}
