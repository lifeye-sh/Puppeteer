package modelendpoint

import "testing"

func TestProxyEndpointPolicy(t *testing.T) {
	for _, raw := range []string{"https://proxy.example:443", "socks5://proxy.example:1080", "socks5h://[::1]:1080/"} {
		if _, e := ParseProxy(raw, false); e != nil {
			t.Errorf("rejected valid proxy %s", raw)
		}
	}
	for _, raw := range []string{"socks5://proxy.example", "socks5://proxy.example:0", "socks5://proxy.example:65536", "socks5://user:secret@proxy.example:1080", "socks5://proxy.example:1080/api", "socks5://proxy.example:1080?", "socks5://proxy.example:1080?q=x", "socks5://proxy.example:1080#fragment", "http://proxy.example:8080", "ftp://proxy.example:21"} {
		if _, e := ParseProxy(raw, false); e == nil {
			t.Errorf("accepted invalid proxy %s", raw)
		}
	}
}
