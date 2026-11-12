package ai

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderBoundaries(t *testing.T) {
	for _, raw := range []string{"http://public.example/v1", "https://user:pass@host/v1", "https://host/v1?key=secret", "https://host/v1#fragment", "file:///tmp/model"} {
		if _, e := ParseEndpoint(raw, false); e == nil {
			t.Fatalf("allowed %s", raw)
		}
	}
	for _, ip := range []string{"169.254.169.254", "0.0.0.0", "::", "fe80::1", "ff02::1"} {
		if allowedIP(net.ParseIP(ip), true) {
			t.Fatalf("allowed %s", ip)
		}
	}
	if allowedIP(net.ParseIP("127.0.0.1"), false) || allowedIP(net.ParseIP("10.0.0.1"), false) || !allowedIP(net.ParseIP("127.0.0.1"), true) {
		t.Fatal("private target policy")
	}
	var mode string
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "redirect":
			http.Redirect(w, r, target.URL, 302)
		case "oversize":
			_, _ = w.Write([]byte(strings.Repeat("x", 262145)))
		case "tools":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"run","tool_calls":[{}]}}]}`))
		case "error":
			http.Error(w, "fixture secret", 500)
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
		}
	}))
	defer model.Close()
	p := Profile{BaseURL: model.URL, Model: "fixture", AllowPrivate: true, MaxOutputTokens: 64}
	for _, m := range []string{"redirect", "oversize", "tools", "error"} {
		mode = m
		if _, e := Generate(context.Background(), p, []Message{}); e != ErrProvider {
			t.Fatalf("accepted %s", m)
		}
	}
	if redirected {
		t.Fatal("redirect followed")
	}
	mode = "ok"
	r, e := Generate(context.Background(), p, []Message{})
	if e != nil || r.Text != "hello" {
		t.Fatal(r, e)
	}
	p.AllowPrivate = false
	if _, e = Generate(context.Background(), p, nil); e == nil {
		t.Fatal("private target accepted")
	}
}
