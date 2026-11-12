package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Delivery errors are deliberately bounded and do not contain credentials or provider responses.
var errTransport = errors.New("notification transport failed")

func transportTLS(c Config) (*tls.Config, error) {
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if c.CA != "" && !roots.AppendCertsFromPEM([]byte(c.CA)) {
		return nil, errors.New("invalid channel CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: c.Host}, nil
}

func sendSMTP(ctx context.Context, c Config, password, to, id, title, body string) error {
	tc, e := transportTLS(c)
	if e != nil {
		return e
	}
	from, e := mail.ParseAddress(c.From)
	if e != nil {
		return errTransport
	}
	if _, e = mail.ParseAddress(to); e != nil || strings.ContainsAny(to+title, "\r\n") {
		return errTransport
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if e != nil {
		return errTransport
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if c.ImplicitTLS {
		secure := tls.Client(conn, tc)
		if secure.HandshakeContext(ctx) != nil {
			return errTransport
		}
		conn = secure
	}
	client, e := smtp.NewClient(conn, c.Host)
	if e != nil {
		return errTransport
	}
	defer client.Close()
	if !c.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP STARTTLS required")
		}
		if client.StartTLS(tc) != nil {
			return errTransport
		}
	}
	if c.Username != "" {
		if client.Auth(smtp.PlainAuth("", c.Username, password, c.Host)) != nil {
			return errTransport
		}
	}
	if client.Mail(from.Address) != nil || client.Rcpt(to) != nil {
		return errTransport
	}
	writer, e := client.Data()
	if e != nil {
		return errTransport
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s@next-terminal.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", c.From, to, title, id, strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	if _, e = io.WriteString(writer, message); e != nil {
		return errTransport
	}
	if writer.Close() != nil {
		return errTransport
	}
	return nil
}

func sendWeCom(ctx context.Context, c Config, secret, to, body string) error {
	if secret == "" || strings.ContainsAny(to, "|\r\n") {
		return errTransport
	}
	base, e := url.Parse(c.APIBase)
	if e != nil || base.Scheme != "https" || base.User != nil {
		return errTransport
	}
	tc, e := transportTLS(c)
	if e != nil {
		return e
	}
	tc.ServerName = base.Hostname()
	transport := &http.Transport{TLSClientConfig: tc}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, q url.Values, payload any, result any) error {
		u := *base
		u.Path = strings.TrimRight(base.Path, "/") + path
		u.RawQuery = q.Encode()
		var buf bytes.Buffer
		if payload != nil {
			if json.NewEncoder(&buf).Encode(payload) != nil {
				return errTransport
			}
		}
		req, e := http.NewRequestWithContext(ctx, method, u.String(), &buf)
		if e != nil {
			return errTransport
		}
		req.Header.Set("Content-Type", "application/json")
		res, e := client.Do(req)
		if e != nil {
			return errTransport
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errTransport
		}
		if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(result) != nil {
			return errTransport
		}
		return nil
	}
	var token struct {
		Code  int    `json:"errcode"`
		Token string `json:"access_token"`
	}
	if call("GET", "/cgi-bin/gettoken", url.Values{"corpid": {c.CorpID}, "corpsecret": {secret}}, nil, &token) != nil || token.Code != 0 || token.Token == "" {
		return errTransport
	}
	var result struct {
		Code    int    `json:"errcode"`
		Invalid string `json:"invaliduser"`
	}
	payload := map[string]any{"touser": to, "msgtype": "text", "agentid": c.AgentID, "text": map[string]string{"content": body}, "enable_duplicate_check": 1, "duplicate_check_interval": 1800}
	if call("POST", "/cgi-bin/message/send", url.Values{"access_token": {token.Token}}, payload, &result) != nil || result.Code != 0 || result.Invalid != "" {
		return errTransport
	}
	return nil
}
