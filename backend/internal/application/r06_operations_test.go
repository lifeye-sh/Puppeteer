package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/modules/connectivity"
	"path/filepath"
	"testing"
	"time"
)

func TestR06MetricsAndKeyRotation(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	r := call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "metrics", "networkScope": connectivity.NetworkScope{CIDRs: []string{"127.0.0.1/32"}, Ports: []int{22}}}, admin, origin)
	expect(t, r, 201)
	r = call(t, srv.URL, "POST", "/api/agent/v1/enroll", connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "metrics", OS: "linux", Arch: "amd64", Version: "test"}, nil, "")
	expect(t, r, 201)
	cfg := agent.Config{Endpoint: srv.URL, AgentID: object(t, r)["agentId"].(string), DeviceKey: object(t, r)["deviceKey"].(string), AllowInsecureDevelopment: true}
	old := cfg.DeviceKey
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.RunConnection(ctx, cfg, nil) }()
	deadline := time.Now().Add(12 * time.Second)
	for {
		r = call(t, srv.URL, "GET", "/api/admin/agent-observations", nil, admin, origin)
		expect(t, r, 200)
		var rows []connectivity.Observation
		_ = json.Unmarshal(r.body, &rows)
		if len(rows) == 1 && rows[0].Metrics != nil {
			if rows[0].Metrics.MemoryTotal == 0 || rows[0].Metrics.CPU == nil || rows[0].Stale {
				t.Fatal("invalid live metrics")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metrics absent")
		}
		time.Sleep(100 * time.Millisecond)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+cfg.AgentID+"/rotate", nil, admin, origin), 204)
	select {
	case e := <-done:
		if !errors.Is(e, agent.ErrRotate) {
			t.Fatal(e)
		}
	case <-time.After(35 * time.Second):
		t.Fatal("rotation not delivered")
	}
	configPath := filepath.Join(t.TempDir(), "agent.json")
	if e := agent.SaveConfig(configPath, cfg); e != nil {
		t.Fatal(e)
	}
	if e := agent.Rotate(ctx, configPath, &cfg); e != nil {
		t.Fatal(e)
	}
	if _, e := a.connectivity.AuthenticateDevice(ctx, old); e == nil {
		t.Fatal("old key remains current")
	}
	ready := make(chan struct{})
	go func() { done <- agent.RunConnection(ctx, cfg, func(int64) { close(ready) }) }()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("new key failed")
	}
	if _, _, _, e := a.connectivity.ClaimAgent(ctx, old); e == nil {
		t.Fatal("old key accepted after confirmation")
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/agent-observations", nil, nil, origin), 401)
}
