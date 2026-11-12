package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/platform/modelendpoint"
	"strings"
	"time"
)

var ErrProvider = errors.New("model_request_failed")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Reply struct {
	ToolCalls        []ToolCall
	UsageKnown       bool
	Text             string
	PromptTokens     int
	CompletionTokens int
}

func ParseEndpoint(raw string, private bool) (*url.URL, error) {
	u, e := modelendpoint.Parse(raw, private)
	if e != nil {
		return nil, ErrProvider
	}
	return u, nil
}

func allowedIP(ip net.IP, private bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return (ip.IsGlobalUnicast() && !ip.IsPrivate()) || (private && (ip.IsPrivate() || ip.IsLoopback()))
}

// Pin the connection to addresses checked immediately before dialing. Redirects,
// environment proxies and link-local metadata endpoints are never followed.
func providerRequest(ctx context.Context, p Profile, messages []Message, stream bool) (*http.Response, func(), error) {
	route, payload := requestPayload(p, messages, stream)
	body, e := json.Marshal(payload)
	if e != nil {
		return nil, nil, ErrProvider
	}
	return providerCall(ctx, p, "POST", route, body)
}
func providerCall(ctx context.Context, p Profile, method, route string, body []byte) (*http.Response, func(), error) {
	u, e := ParseEndpoint(p.BaseURL, p.AllowPrivate)
	if e != nil {
		return nil, nil, ErrProvider
	}
	transport := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second, MaxResponseHeaderBytes: 32768}
	transport.DialContext = func(c context.Context, network, address string) (net.Conn, error) {
		return dialProvider(c, p, u, address)
	}
	client := &http.Client{Transport: transport, Timeout: 55 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrProvider }}
	u.Path = strings.TrimRight(u.Path, "/") + route
	u.RawPath = ""
	req, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if e != nil {
		transport.CloseIdleConnections()
		return nil, nil, ErrProvider
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		if p.Protocol == "anthropic" {
			req.Header.Set("x-api-key", p.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
	}
	if p.Protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	res, e := client.Do(req)
	if e != nil {
		transport.CloseIdleConnections()
		return nil, nil, ErrProvider
	}
	cleanup := func() { res.Body.Close(); transport.CloseIdleConnections() }
	if res.StatusCode != 200 {
		cleanup()
		return nil, nil, ErrProvider
	}
	return res, cleanup, nil
}

func Generate(ctx context.Context, p Profile, messages []Message) (Reply, error) {
	res, cleanup, e := providerRequest(ctx, p, messages, false)
	if e != nil {
		return Reply{}, e
	}
	defer cleanup()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 262145))
	if e != nil || len(raw) > 262144 {
		return Reply{}, ErrProvider
	}
	return parseCompletion(raw, p)
}
