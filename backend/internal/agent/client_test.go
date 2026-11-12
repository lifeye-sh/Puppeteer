package agent

import "testing"

func TestEndpointSecurity(t *testing.T) {
	for _, endpoint := range []string{"http://example.org", "https://user:pass@example.org", "https://example.org/path", "https://example.org?token=secret", "https://example.org#fragment", "file:///tmp/test"} {
		if _, e := (Config{Endpoint: endpoint}).URL(); e == nil {
			t.Fatal(endpoint)
		}
	}
	if u, e := (Config{Endpoint: "https://example.org/"}).URL(); e != nil || u != "wss://example.org/api/agent/v1/connect" {
		t.Fatal(u, e)
	}
	if _, e := (Config{Endpoint: "http://127.0.0.1:8089", AllowInsecureDevelopment: true}).URL(); e != nil {
		t.Fatal(e)
	}
}
