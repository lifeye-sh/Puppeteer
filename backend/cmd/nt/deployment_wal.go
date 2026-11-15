package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"puppeteer/backend/internal/platform/deployment"
	"puppeteer/backend/internal/platform/runtimeprofile"
	"os"
	"time"
)

func deploymentWALGuard() error {
	if deployment.LoadSecrets() != nil {
		return errors.New("runtime secrets unavailable")
	}
	p, err := runtimeprofile.FromEnvironment()
	if err != nil || !p.WritesEnabled {
		return errors.New("restore quarantine blocks WAL maintenance")
	}
	u, err := url.Parse(p.DatabaseURL)
	if err != nil || u.Hostname() != "postgres" || (u.Port() != "" && u.Port() != "5432") || u.Path != "/puppeteer" {
		return errors.New("active database is not the enrolled PostgreSQL database")
	}
	_, port, err := net.SplitHostPort(os.Getenv("NT_ADDR"))
	if err != nil {
		return errors.New("management address unavailable")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/readyz")
	if err != nil {
		return errors.New("live application unavailable")
	}
	defer res.Body.Close()
	var ready struct {
		Status string `json:"status"`
		Frozen *bool  `json:"frozen"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&ready) != nil || ready.Status != "ready" || ready.Frozen == nil || *ready.Frozen {
		return errors.New("application is not ready for WAL maintenance")
	}
	return nil
}
