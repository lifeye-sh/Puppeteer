package application

import (
	"context"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/modules/connectivity"
	"testing"
	"time"
)

func TestR06AgentChannelEpochHeartbeatAndRevoke(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	r := call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "channel", "networkScope": map[string]any{"cidrs": []string{"10.0.0.0/24"}, "ports": []int{22}}}, admin, origin)
	expect(t, r, 201)
	r = call(t, srv.URL, "POST", "/api/agent/v1/enroll", connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "channel", OS: "linux", Arch: "amd64", Version: "test"}, nil, "")
	expect(t, r, 201)
	cfg := agent.Config{Endpoint: srv.URL, AgentID: object(t, r)["agentId"].(string), DeviceKey: object(t, r)["deviceKey"].(string), AllowInsecureDevelopment: true}
	start := func() (int64, <-chan error, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan int64, 1)
		done := make(chan error, 1)
		go func() { done <- agent.RunConnection(ctx, cfg, func(e int64) { ready <- e }) }()
		t.Cleanup(cancel)
		select {
		case epoch := <-ready:
			return epoch, done, cancel
		case e := <-done:
			t.Fatalf("connect: %v", e)
		case <-time.After(8 * time.Second):
			t.Fatal("hello timeout")
		}
		return 0, nil, cancel
	}
	waitStopped := func(done <-chan error) {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("stale channel stayed open")
		}
	}
	first, old, _ := start()
	second, current, cancel := start()
	if second <= first {
		t.Fatal("epoch did not increase")
	}
	waitStopped(old)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var beat bool
		e := p.QueryRow(context.Background(), `SELECT last_heartbeat_at IS NOT NULL FROM agent_gateway_configs WHERE id=$1`, cfg.AgentID).Scan(&beat)
		if e != nil {
			t.Fatal(e)
		}
		if beat {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat absent")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !a.connectivity.AgentOnline(cfg.AgentID) {
		t.Fatal("live channel offline")
	}
	cancel()
	waitStopped(current)
	_, active, _ := start()
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+cfg.AgentID+"/revoke", nil, admin, origin), 204)
	waitStopped(active)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if e := agent.RunConnection(ctx, cfg, nil); e == nil {
		t.Fatal("revoked key reconnected")
	}
	if e := a.connectivity.CheckAgentEpoch(context.Background(), cfg.AgentID, second); e == nil {
		t.Fatal("old epoch revived")
	}
}
