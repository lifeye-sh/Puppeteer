package netproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"golang.org/x/net/proxy"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// Proxy authentication is sent only to the configured proxy, never the target.
func Dial(ctx context.Context, u *url.URL, target string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	host := u.Host
	if u.Port() == "" {
		port := "1080"
		if u.Scheme == "http" {
			port = "80"
		}
		if u.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		var auth *proxy.Auth
		if u.User != nil {
			p, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: p}
		}
		pd, e := proxy.SOCKS5("tcp", host, auth, d)
		if e != nil {
			return nil, errors.New("invalid SOCKS proxy")
		}
		if u.Scheme == "socks5" {
			h, p, e := net.SplitHostPort(target)
			if e != nil {
				return nil, e
			}
			ips, e := net.DefaultResolver.LookupIPAddr(ctx, h)
			if e != nil || len(ips) == 0 {
				return nil, errors.New("target DNS failed")
			}
			target = net.JoinHostPort(ips[0].IP.String(), p)
		}
		return pd.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("unsupported proxy")
	}
	raw, e := d.DialContext(ctx, "tcp", host)
	if e != nil {
		return nil, errors.New("proxy unavailable")
	}
	ok := false
	defer func() {
		if !ok {
			raw.Close()
		}
	}()
	baseConn := raw
	stop := context.AfterFunc(ctx, func() { baseConn.Close() })
	defer stop()
	if deadline, yes := ctx.Deadline(); yes {
		_ = raw.SetDeadline(deadline)
	} else {
		_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if u.Scheme == "https" {
		secure := tls.Client(raw, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if e = secure.HandshakeContext(ctx); e != nil {
			return nil, errors.New("proxy TLS failed")
		}
		raw = secure
	}
	req := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if u.User != nil {
		p, _ := u.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+p)))
	}
	if e = req.Write(raw); e != nil {
		return nil, errors.New("proxy CONNECT failed")
	}
	reader := bufio.NewReaderSize(raw, 4096)
	// Limit headers to 16 KiB without consuming any tunneled bytes.
	var header strings.Builder
	for header.Len() < 16384 {
		chunk, e := reader.ReadSlice('\n')
		line := string(chunk)
		if e != nil {
			return nil, errors.New("proxy response failed")
		}
		header.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	if header.Len() >= 16384 {
		return nil, errors.New("proxy headers too large")
	}
	res, e := http.ReadResponse(bufio.NewReader(strings.NewReader(header.String())), req)
	if e != nil || res.StatusCode != 200 {
		return nil, errors.New("proxy CONNECT rejected")
	}
	_ = raw.SetDeadline(time.Time{})
	ok = true
	return &bufferedConn{Conn: raw, r: reader}, nil
}
