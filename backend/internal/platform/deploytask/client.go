package deploytask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

type Client struct{ Socket, Token string }

func (c Client) Call(ctx context.Context, method, path string, input any) (int, json.RawMessage, error) {
	if c.Socket == "" || len(c.Token) < 32 {
		return 0, nil, errors.New("deployment executor is not enrolled")
	}
	if !(method == "GET" && path == "/v1/status" || method == "POST" && (path == "/v1/preflight" || path == "/v1/tasks")) {
		return 0, nil, errors.New("unsupported deployment operation")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return 0, nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", c.Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, "http://deployd"+path, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, errors.New("deployment executor unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if err != nil || len(body) > 1048576 || !json.Valid(body) {
		return 0, nil, errors.New("invalid deployment executor response")
	}
	return response.StatusCode, body, nil
}
