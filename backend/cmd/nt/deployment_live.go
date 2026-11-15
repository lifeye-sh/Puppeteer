package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"puppeteer/backend/internal/platform/deploytask"
	"os"
	"strings"
	"time"
)

func deploymentLive(raw, operation string) error {
	if operation != "check" && operation != "site-apply" && operation != "verify" {
		return errors.New("invalid operation")
	}
	var request deploytask.Request
	if json.Unmarshal([]byte(raw), &request) != nil || deploytask.Validate(request) != nil {
		return errors.New("invalid deployment request")
	}
	token, e := os.ReadFile(os.Getenv("NT_DEPLOY_TOKEN_FILE"))
	if e != nil {
		return errors.New("executor credential unavailable")
	}
	address := os.Getenv("NT_ADDR")
	_, port, e := net.SplitHostPort(address)
	if e != nil {
		return errors.New("management address unavailable")
	}
	req, e := http.NewRequest("POST", "http://"+net.JoinHostPort("127.0.0.1", port)+"/api/internal/deployment-"+operation, bytes.NewBufferString(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return errors.New("live application check unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("live application check denied")
	}
	result, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil || len(result) > 65536 || !json.Valid(result) {
		return errors.New("invalid live application check")
	}
	_, e = os.Stdout.Write(result)
	return e
}
