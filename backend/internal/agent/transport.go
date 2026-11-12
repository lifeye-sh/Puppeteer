package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/platform/netproxy"
	"os"
	"regexp"
	"strings"
)

type headerTransport struct {
	base    http.RoundTripper
	host    string
	headers map[string]string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != t.host {
		return nil, errors.New("endpoint host changed")
	}
	v := r.Clone(r.Context())
	for k, val := range t.headers {
		v.Header.Set(k, val)
	}
	return t.base.RoundTrip(v)
}
func (c Config) Client() (*http.Client, error) {
	if c.HeartbeatSeconds != 0 && (c.HeartbeatSeconds < 10 || c.HeartbeatSeconds > 60) {
		return nil, errors.New("heartbeatSeconds must be 10..60")
	}
	if c.SchemaVersion != 0 && c.SchemaVersion != 1 {
		return nil, errors.New("unsupported config schema")
	}
	if c.CollectionSeconds != 0 && (c.CollectionSeconds < 5 || c.CollectionSeconds > 300) {
		return nil, errors.New("collectionSeconds must be 5..300")
	}
	for _, p := range c.DiskPaths {
		if len(p) > 512 || !strings.HasPrefix(p, "/") {
			return nil, errors.New("disk paths must be absolute")
		}
	}
	if _, e := c.URL(); e != nil {
		return nil, e
	}
	if len(c.DiskPaths) > 32 || len(c.NetworkPattern) > 512 || len(c.Headers) > 16 {
		return nil, errors.New("configuration exceeds limits")
	}
	if _, e := regexp.Compile(c.NetworkPattern); e != nil {
		return nil, e
	}
	for k, v := range c.Headers {
		key := http.CanonicalHeaderKey(k)
		if key == "X-Nt-Agent-Key" || key == "Authorization" || key == "Proxy-Authorization" || key == "Upgrade" || key == "Content-Length" || key == "Transfer-Encoding" || key == "Cookie" || key == "Host" || key == "Connection" || strings.HasPrefix(key, "Sec-Websocket") || strings.ContainsAny(k+v, "\r\n") || len(v) > 4096 {
			return nil, errors.New("reserved or invalid custom header")
		}
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		b, e := os.ReadFile(c.CAFile)
		if e != nil {
			return nil, e
		}
		pool, e := x509.SystemCertPool()
		if e != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid CA file")
		}
		tc.RootCAs = pool
	}
	if c.CertFile != "" || c.KeyFile != "" {
		cert, e := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if e != nil {
			return nil, e
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tc
	tr.Proxy = nil
	if c.ProxyURL != "" {
		u, e := url.Parse(c.ProxyURL)
		if e != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid proxy")
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, errors.New("invalid proxy scheme")
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) { return netproxy.Dial(ctx, u, addr) }
	}
	headers := map[string]string{}
	for k, v := range c.Headers {
		headers[k] = v
	}
	if c.BasicUsername != "" {
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(c.BasicUsername+":"+c.BasicPassword))
	}
	endpoint, _ := url.Parse(c.Endpoint)
	return &http.Client{Transport: headerTransport{tr, endpoint.Host, headers}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}, nil
}

func DeviceAuth(h http.Header, c Config) {
	if c.BasicUsername != "" {
		h.Set("X-NT-Agent-Key", c.DeviceKey)
	} else {
		h.Set("Authorization", "Bearer "+c.DeviceKey)
	}
}
