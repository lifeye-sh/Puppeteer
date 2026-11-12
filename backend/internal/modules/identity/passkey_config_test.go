package identity

import "testing"

func TestPasskeyConfigBoundary(t *testing.T) {
	for _, v := range []struct {
		rp, origin string
		valid      bool
	}{
		{"example.com", "https://login.example.com", true},
		{"backend.localhost", "https://backend.localhost:5174", true},
		{"com", "https://example.com", false},
		{"co.uk", "https://test.co.uk", false},
		{"example.com", "https://evil-example.com", false},
		{"example.com", "https://example.com.evil.com", false},
		{"example.com", "http://example.com", false},
		{"example.com", "https://example.com/", false},
		{"example.com", "https://user@example.com", false},
		{"example.com", "https://example.com?", false},
		{"example.com", "https://example.com:99999", false},
		{"https://example.com", "https://example.com", false},
		{"127.0.0.1", "https://127.0.0.1", false},
	} {
		t.Run(v.rp+v.origin, func(t *testing.T) {
			if e := ValidatePasskeyConfig(v.rp, []string{v.origin}); (e == nil) != v.valid {
				t.Fatalf("valid=%v error=%v", v.valid, e)
			}
		})
	}
	if ValidatePasskeyConfig("example.com", nil) == nil {
		t.Fatal("empty allowlist accepted")
	}
	if ValidatePasskeyConfig("example.com", []string{"https://example.com", "https://example.com"}) == nil {
		t.Fatal("duplicate origins accepted")
	}
}
