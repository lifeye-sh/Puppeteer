package ai

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/platform/modelendpoint"
	"next-terminal/backend/internal/platform/netproxy"
	"sort"
	"time"
)

func addresses(ctx context.Context, u *url.URL, private bool) ([]net.IPAddr, error) {
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if e != nil || len(ips) == 0 || len(ips) > 16 {
		return nil, ErrProvider
	}
	for _, v := range ips {
		if v.Zone != "" || !allowedIP(v.IP, private) || (u.Scheme == "http" && !v.IP.IsPrivate() && !v.IP.IsLoopback()) {
			return nil, ErrProvider
		}
	}
	return ips, nil
}
func endpointPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
func dialProvider(ctx context.Context, p Profile, target *url.URL, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil || host != target.Hostname() || port != endpointPort(target) {
		return nil, ErrProvider
	}
	ips, e := addresses(ctx, target, p.AllowPrivate)
	if e != nil {
		return nil, e
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	if p.ProxyURL == "" {
		for _, v := range ips {
			c, e := d.DialContext(ctx, "tcp", net.JoinHostPort(v.IP.String(), port))
			if e == nil {
				return c, nil
			}
		}
		return nil, ErrProvider
	}
	proxy, e := modelendpoint.ParseProxy(p.ProxyURL, p.AllowPrivate)
	if e != nil || (proxy.Path != "" && proxy.Path != "/") {
		return nil, ErrProvider
	}
	proxies, e := addresses(ctx, proxy, p.AllowPrivate)
	if e != nil {
		return nil, e
	}
	if proxy.Scheme == "socks5" || proxy.Scheme == "socks5h" {
		// Both hops are pinned after our address policy check. Never delegate
		// target DNS to the proxy, which could bypass private/metadata checks.
		for _, hop := range proxies {
			pinned := *proxy
			pinned.Host = net.JoinHostPort(hop.IP.String(), proxy.Port())
			for _, targetIP := range ips {
				conn, err := netproxy.Dial(ctx, &pinned, net.JoinHostPort(targetIP.IP.String(), port))
				if err == nil {
					return conn, nil
				}
			}
		}
		return nil, ErrProvider
	}
	// CONNECT uses a validated target IP, never proxy-side DNS. TLS to the model
	// remains handled by http.Transport using the original model hostname.
	for _, v := range proxies {
		conn, e := d.DialContext(ctx, "tcp", net.JoinHostPort(v.IP.String(), endpointPort(proxy)))
		if e != nil {
			continue
		}
		if proxy.Scheme == "https" {
			secure := tls.Client(conn, &tls.Config{ServerName: proxy.Hostname(), MinVersion: tls.VersionTLS12})
			if e = secure.HandshakeContext(ctx); e != nil {
				conn.Close()
				continue
			}
			conn = secure
		}
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		targetIP := net.JoinHostPort(ips[0].IP.String(), port)
		if _, e = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetIP, targetIP); e != nil {
			conn.Close()
			continue
		}
		reader := bufio.NewReader(io.LimitReader(conn, 32768))
		res, e := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
		if e != nil || res.StatusCode != 200 || reader.Buffered() != 0 {
			conn.Close()
			continue
		}
		conn.SetDeadline(time.Time{})
		return conn, nil
	}
	return nil, ErrProvider
}
func Models(ctx context.Context, p Profile) ([]string, error) {
	res, close, e := providerCall(ctx, p, "GET", "/models", nil)
	if e != nil {
		return nil, e
	}
	defer close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 262145))
	if e != nil || len(raw) > 262144 {
		return nil, ErrProvider
	}
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil, ErrProvider
	}
	out := []string{}
	seen := map[string]bool{}
	for _, m := range v.Data {
		if len(m.ID) > 0 && len(m.ID) <= 200 && !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m.ID)
		}
		if len(out) >= 200 {
			break
		}
	}
	sort.Strings(out)
	return out, nil
}
