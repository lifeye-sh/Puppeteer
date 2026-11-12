package nativeclient

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func localName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// RFC 6761 names are loopback-only even on systems whose DNS resolver does not
// recognize *.localhost. Change only dialing, never the HTTP Host or TLS name.
func LocalhostTransport(base *http.Transport) *http.Transport {
	transport := base.Clone()
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e == nil && localName(host) {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return dial(ctx, network, address)
	}
	proxy := transport.Proxy
	transport.Proxy = func(r *http.Request) (*url.URL, error) {
		if localName(r.URL.Hostname()) || proxy == nil {
			return nil, nil
		}
		return proxy(r)
	}
	return transport
}
