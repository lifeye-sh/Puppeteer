package nativeclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestLocalhostTransport(t *testing.T) {
	for _, host := range []string{"localhost", "backend.localhost", "BACKEND.LOCALHOST."} {
		if !localName(host) {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"localhost.example", "backendlocalhost", "backend.localhost.evil", "127.0.0.1"} {
		if localName(host) {
			t.Fatal(host)
		}
	}
	called := ""
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(_ context.Context, _ string, address string) (net.Conn, error) {
		called = address
		return nil, errors.New("capture only")
	}
	base.Proxy = func(*http.Request) (*url.URL, error) { return url.Parse("http://proxy.example:8080") }
	transport := LocalhostTransport(base)
	_, _ = transport.DialContext(context.Background(), "tcp", "backend.localhost:5174")
	if called != "127.0.0.1:5174" {
		t.Fatal(called)
	}
	_, _ = transport.DialContext(context.Background(), "tcp", "platform.example:443")
	if called != "platform.example:443" {
		t.Fatal(called)
	}
	req, _ := http.NewRequest("GET", "https://backend.localhost:5174/api", nil)
	proxy, _ := transport.Proxy(req)
	if proxy != nil || req.URL.Hostname() != "backend.localhost" {
		t.Fatal("loopback proxy or TLS name changed")
	}
	req, _ = http.NewRequest("GET", "https://platform.example/api", nil)
	proxy, _ = transport.Proxy(req)
	if proxy == nil {
		t.Fatal("normal proxy lost")
	}
}
