package website

import "testing"

func TestTrustedProxyChain(t *testing.T) {
	for _, v := range []struct {
		peer, xff string
		trust     []string
		want      string
	}{
		{"192.0.2.1:1234", "1.2.3.4", nil, "192.0.2.1"},
		{"10.0.0.2:443", "1.2.3.4, 192.0.2.1, 10.0.0.1", []string{"10.0.0.0/24"}, "192.0.2.1"},
		{"10.0.0.2:443", "invalid, 10.0.0.1", []string{"10.0.0.0/24"}, "10.0.0.2"},
	} {
		if got := ClientIP(v.peer, v.xff, v.trust); got != v.want {
			t.Fatalf("%s != %s", got, v.want)
		}
	}
}
func TestSafeReturnPaths(t *testing.T) {
	for _, path := range []string{"//evil.test", "https://evil.test", "/\\evil.test", "/%2f%2fevil.test", "/%5cevil.test", "/\r\nLocation: evil"} {
		if SafePath(path) {
			t.Error(path)
		}
	}
	if !SafePath("/docs?q=hello") {
		t.Fatal("relative return rejected")
	}
}
