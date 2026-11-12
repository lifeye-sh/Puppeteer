package identity

import "testing"

func TestNativeRedirectBoundary(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:1024/callback", "http://127.0.0.1:65535/callback"} {
		if !NativeRedirect(raw) {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"https://127.0.0.1:1234/callback", "http://localhost:1234/callback", "http://127.0.0.2:1234/callback", "http://[::1]:1234/callback", "http://127.0.0.1:80/callback", "http://127.0.0.1:65536/callback", "http://127.0.0.1:01234/callback", "http://127.0.0.1:1234/%63allback", "http://127.0.0.1:1234/callback?", "http://127.0.0.1:1234/callback#", "http://127.0.0.1:1234/callback?q=x", "http://user@127.0.0.1:1234/callback", "http://127.0.0.1:1234/callback/", "http://127.0.0.1:1234/callback\n"} {
		if NativeRedirect(raw) {
			t.Errorf("accepted unsafe callback %q", raw)
		}
	}
}
