package application

import (
	"net/http/httptest"
	"testing"
)

func TestRequestOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, host, origin string
		dynamic, want      bool
	}{
		{"public IP port", "8.222.163.55:8088", "https://8.222.163.55:8088", true, true},
		{"new domain", "console.example.com:9443", "https://console.example.com:9443", true, true},
		{"IPv6", "[2001:db8::1]:8088", "https://[2001:db8::1]:8088", true, true},
		{"default port", "console.example.com:443", "https://CONSOLE.example.com", true, true},
		{"other site", "console.example.com", "https://evil.example", true, false},
		{"other port", "console.example.com:8088", "https://console.example.com", true, false},
		{"downgrade", "console.example.com", "http://console.example.com", true, false},
		{"missing", "console.example.com", "", true, false},
		{"opaque", "console.example.com", "null", true, false},
		{"userinfo", "console.example.com", "https://user@console.example.com", true, false},
		{"path", "console.example.com", "https://console.example.com/path", true, false},
		{"disabled", "console.example.com", "https://console.example.com", false, false},
		{"explicit compatibility", "private:8443", "https://configured.example", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{config: Config{SecureCookies: true, AllowRequestOrigin: tc.dynamic, AllowedOrigins: []string{"https://configured.example"}}}
			r := httptest.NewRequest("POST", "http://backend/api/test", nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-Host", "evil.example")
			r.Header.Set("Forwarded", "host=evil.example;proto=https")
			if got := a.allowsBrowserOrigin(r); got != tc.want {
				t.Fatalf("allowed=%v want %v", got, tc.want)
			}
		})
	}
}
