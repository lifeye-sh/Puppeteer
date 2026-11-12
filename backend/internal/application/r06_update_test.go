package application

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/agentupdate"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestR06ManagedUpdateAndUnhealthyRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("real process update test")
	}
	_, a, srv := r02Server(t, false)
	host, port, _ := sshTargetRoot(t, t.TempDir())
	admin := loginAs(t, srv, "accept-admin")
	dir := t.TempDir()
	binary := filepath.Join(dir, "nt-agent")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/nt-agent")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if raw, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, raw)
	}
	repo, keys := filepath.Join(dir, "repo"), filepath.Join(dir, "keys")
	if e := agentupdate.InitRepository(repo, keys); e != nil {
		t.Fatal(e)
	}
	if e := agentupdate.Publish(repo, keys, binary, agentupdate.Release{Sequence: 1, Version: "good", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	repository := httptest.NewServer(http.FileServer(http.Dir(repo)))
	defer repository.Close()
	r := call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "update", "networkScope": connectivity.NetworkScope{CIDRs: []string{host + "/32"}, Ports: []int{port}}}, admin, origin)
	expect(t, r, 201)
	r = call(t, srv.URL, "POST", "/api/agent/v1/enroll", connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "update", OS: "linux", Arch: "amd64", Version: "test"}, nil, "")
	expect(t, r, 201)
	state := filepath.Join(dir, "state")
	cfg := agent.Config{Endpoint: srv.URL, AgentID: object(t, r)["agentId"].(string), DeviceKey: object(t, r)["deviceKey"].(string), AllowInsecureDevelopment: true, Updates: &agentupdate.Config{Repository: repository.URL, RootFile: filepath.Join(repo, "1.root.json"), StateDir: state, AllowHTTPDevelopment: true}}
	configPath := filepath.Join(dir, "agent.json")
	if e := agent.SaveConfig(configPath, cfg); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "--supervise", "--config", configPath)
	// Let the supervisor run its child cleanup. CommandContext's default SIGKILL
	// leaves an orphan worker polling the test server and blocks fixture cleanup.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	waitAt := func(state, file string, predicate func([]byte) bool) {
		deadline := time.Now().Add(65 * time.Second)
		for {
			raw, e := os.ReadFile(filepath.Join(state, file))
			if e == nil && predicate(raw) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s", file)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	wait := func(file string, predicate func([]byte) bool) { waitAt(state, file, predicate) }
	wait("worker-health.json", func([]byte) bool { return true })
	// A second real supervisor stays untouched until the first canary is healthy.
	r = call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "second", "networkScope": connectivity.NetworkScope{CIDRs: []string{host + "/32"}, Ports: []int{port}}}, admin, origin)
	expect(t, r, 201)
	r = call(t, srv.URL, "POST", "/api/agent/v1/enroll", connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "second", OS: "linux", Arch: "amd64", Version: "test"}, nil, "")
	expect(t, r, 201)
	secondState := filepath.Join(dir, "second-state")
	second := cfg
	second.AgentID = object(t, r)["agentId"].(string)
	second.DeviceKey = object(t, r)["deviceKey"].(string)
	updates := *cfg.Updates
	updates.StateDir = secondState
	second.Updates = &updates
	secondConfig := filepath.Join(dir, "second.json")
	if e := agent.SaveConfig(secondConfig, second); e != nil {
		t.Fatal(e)
	}
	secondBinary := filepath.Join(dir, "second-agent")
	original, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(secondBinary, original, 0700); e != nil {
		t.Fatal(e)
	}
	secondCmd := exec.CommandContext(ctx, secondBinary, "--supervise", "--config", secondConfig)
	secondCmd.Cancel = func() error { return secondCmd.Process.Signal(os.Interrupt) }
	secondCmd.WaitDelay = 5 * time.Second
	if e = secondCmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); _ = secondCmd.Wait() })
	waitAt(secondState, "worker-health.json", func([]byte) bool { return true })
	assetID := r04Asset(t, srv, admin, host, port, true)
	r = call(t, srv.URL, "GET", "/api/admin/assets/"+assetID, nil, admin, origin)
	var asset resource.Asset
	_ = json.Unmarshal(r.body, &asset)
	asset.AgentID = cfg.AgentID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+assetID, asset, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+assetID+"/host-key/discover", nil, admin, origin), 200)
	session, ticket := reserve(t, srv, admin, assetID, false)
	ws := attach(t, srv, admin, session, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+session)
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-tick.C:
				if ws.WriteMessage(websocket.TextMessage, []byte("9heartbeat")) != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() { stopHeartbeat(); <-heartbeatDone })
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+cfg.AgentID+"/update", map[string]string{"action": "install"}, admin, origin), 204)
	wait("status.json", func(raw []byte) bool {
		var s agentupdate.Status
		_ = json.Unmarshal(raw, &s)
		return s.State == "draining"
	})
	deadline := time.Now().Add(5 * time.Second)
	for a.connectivity.AgentDialReady(cfg.AgentID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if a.connectivity.AgentDialReady(cfg.AgentID) {
		t.Fatal("draining node still accepts new streams")
	}
	if !a.connectivity.AgentDialReady(second.AgentID) {
		t.Fatal("canary drain affected second node")
	}
	stopHeartbeat()
	<-heartbeatDone
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	ws.Close()
	waitClosed(t, a, session)
	wait("status.json", func(raw []byte) bool {
		var s agentupdate.Status
		_ = json.Unmarshal(raw, &s)
		return s.State == "installed" && s.Sequence == 1
	})
	if raw, _ := os.ReadFile(filepath.Join(secondState, "status.json")); len(raw) > 0 {
		var s agentupdate.Status
		_ = json.Unmarshal(raw, &s)
		if s.State == "installed" {
			t.Fatal("unrequested node updated")
		}
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+second.AgentID+"/update", map[string]string{"action": "install"}, admin, origin), 204)
	waitAt(secondState, "status.json", func(raw []byte) bool {
		var s agentupdate.Status
		_ = json.Unmarshal(raw, &s)
		return s.State == "installed" && s.Sequence == 1
	})
	bad := filepath.Join(dir, "bad-agent")
	if e := os.WriteFile(bad, []byte("#!/bin/sh\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := agentupdate.Publish(repo, keys, bad, agentupdate.Release{Sequence: 2, Version: "unhealthy", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+cfg.AgentID+"/update", map[string]string{"action": "install"}, admin, origin), 204)
	wait("status.json", func(raw []byte) bool {
		var s agentupdate.Status
		_ = json.Unmarshal(raw, &s)
		return s.State == "rolled_back" && s.Sequence == 2
	})
	wait("worker-health.json", func([]byte) bool { return true })
	raw, e := os.ReadFile(binary)
	if e != nil || len(raw) < 4 || string(raw[:4]) != "\x7fELF" {
		t.Fatal("original executable not restored")
	}
	if !a.connectivity.AgentDialReady(second.AgentID) {
		t.Fatal("first node rollback interrupted second node")
	}
}
