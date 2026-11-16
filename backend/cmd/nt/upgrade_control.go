package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func upgradeControl(operation, actor string) error {
	if operation != "check" && operation != "drain" && operation != "resume" && operation != "ready" {
		return errors.New("invalid upgrade control")
	}
	_, port, e := net.SplitHostPort(os.Getenv("NT_ADDR"))
	if e != nil {
		return errors.New("management address unavailable")
	}
	method, path := http.MethodPost, "/api/internal/deployment-upgrade-"+operation
	raw, _ := json.Marshal(map[string]string{"actor": actor})
	if operation == "ready" {
		method, path = http.MethodGet, "/readyz"
		raw = nil
	}
	req, e := http.NewRequest(method, "http://"+net.JoinHostPort("127.0.0.1", port)+path, bytes.NewReader(raw))
	if e != nil {
		return e
	}
	if operation != "ready" {
		token, e := os.ReadFile(os.Getenv("NT_DEPLOY_TOKEN_FILE"))
		if e != nil {
			return errors.New("executor credential unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 50 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return errors.New("upgrade control unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("upgrade control denied")
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil || len(body) > 65536 || !json.Valid(body) {
		return errors.New("invalid upgrade response")
	}
	_, e = os.Stdout.Write(body)
	return e
}
