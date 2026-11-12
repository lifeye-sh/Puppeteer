package application

import (
	"context"
	"encoding/json"
	"fmt"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/modules/connectivity"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestR16AgentEndpointConfigurationAndInstaller(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("agent-default-member", nil, nil), admin)
	member := loginAs(t, srv, "agent-default-member")
	expect(t, call(t, srv.URL, "GET", "/api/admin/agent-installations/config", nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "GET", "/api/admin/agent-installations/config", nil, member, origin), 403)
	initial, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []any{"http://backend.example", "https://user:password@backend.example", "https://backend.example/api", "https://backend.example/%2f", "https://backend.example?", "https://backend.example#", "https://backend.example:0", "https://backend.example:65536", "https://backend.example:", "https://bad_host.example", nil, true} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(initial.ID), map[string]any{"set": map[string]any{"agent-gateway-endpoint": bad}}, admin)
		expect(t, r, 422)
	}
	endpoint := "https://agent.example:8443"
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(initial.ID), map[string]any{"set": map[string]any{"agent-gateway-endpoint": endpoint}}, admin)
	expect(t, r, 202)
	view := object(t, call(t, srv.URL, "GET", "/api/admin/agent-installations/config", nil, admin, origin))
	if view["endpoint"] != endpoint || view["revision"].(float64) <= float64(initial.ID) {
		t.Fatal("default endpoint not published", view)
	}
	a.config.AgentBinaryPath = filepath.Join(t.TempDir(), "agent")
	if e = os.WriteFile(a.config.AgentBinaryPath, []byte("fixture-binary"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, override := range []string{"", "https://explicit.example:9443"} {
		release := make(chan struct{})
		close(release)
		host, port, received := installerTarget(t, true, release)
		label := fmt.Sprintf("endpoint fixture %d", port)
		cred := postID(t, srv, "/api/admin/credentials", credInput(label), admin)
		body := assetInput(label, cred, "")
		body["host"], body["port"] = host, port
		id := postID(t, srv, "/api/admin/assets", body, admin)
		observation := object(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin))
		key := observation["observed"].(map[string]any)
		key["assetVersion"] = observation["assetVersion"]
		expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id+"/host-key", key, admin, origin), 204)
		in := agentInstallInput{AssetID: id, Name: "endpoint fixture", Endpoint: override, Scope: connectivity.NetworkScope{CIDRs: []string{"127.0.0.1/32"}, Ports: []int{22}}}
		r = call(t, srv.URL, "POST", "/api/admin/agent-installations", in, admin, origin)
		expect(t, r, 202)
		var config agent.Config
		select {
		case config = <-received:
		case <-time.After(10 * time.Second):
			t.Fatal("no private installation config")
		}
		want := endpoint
		if override != "" {
			want = override
		}
		if config.Endpoint != want {
			t.Fatal("installer endpoint mismatch", config.Endpoint)
		}
		// The fixture intentionally fails systemd; the worker must revoke its
		// temporary device identity and finish before test schema cleanup.
		job := object(t, r)["id"].(string)
		deadline := time.Now().Add(5 * time.Second)
		for {
			var state string
			if e = a.db.Row(ctx, `SELECT state FROM agent_installations WHERE id=$1`, job).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if state == "failed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("installer did not finish")
			}
			time.Sleep(25 * time.Millisecond)
		}
		if _, e = a.connectivity.AuthenticateDevice(ctx, config.DeviceKey); e == nil {
			t.Fatal("failed installation key retained")
		}
	}
}

func TestR16ResourceGroupsAreLiveReadonlyProjections(t *testing.T) {
	_, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	initial, e := a.system.Configuration(ctx, 0)
	if e != nil {
		t.Fatal(e)
	}
	root := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "projection root"}, admin)
	child := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "projection child", "parentId": root}, admin)
	view := object(t, call(t, srv.URL, "GET", "/api/settings", nil, admin, origin))
	projection := view["readOnly"].(map[string]any)
	var tree []struct {
		ID       string `json:"id"`
		Children []struct {
			ID string `json:"id"`
		} `json:"children"`
	}
	if json.Unmarshal([]byte(projection["asset-group-tree"].(string)), &tree) != nil || len(tree) != 1 || tree[0].ID != root || len(tree[0].Children) != 1 || tree[0].Children[0].ID != child {
		t.Fatal("incorrect authoritative tree", projection)
	}
	if projection["asset-group-tree"] != projection["website-group-tree"] {
		t.Fatal("shared catalog diverged")
	}
	if view["id"].(float64) != float64(initial.ID) {
		t.Fatal("resource edit created settings revision")
	}
	r, _ := settingsRequest(t, srv.URL, "PUT", "/api/admin/properties", configTag(initial.ID), map[string]any{"asset-group-tree": projection["asset-group-tree"], "website-group-tree": projection["website-group-tree"]}, admin)
	expect(t, r, 204)
	for _, key := range []string{"asset-group-tree", "website-group-tree"} {
		r, _ = settingsRequest(t, srv.URL, "PUT", "/api/admin/properties", configTag(initial.ID), map[string]any{key: "[]"}, admin)
		expect(t, r, 422)
		r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(initial.ID), map[string]any{"set": map[string]any{key: projection[key]}}, admin)
		expect(t, r, 422)
		if _, ok := view["values"].(map[string]any)[key]; ok {
			t.Fatal("current group metadata leaked into history values")
		}
	}
}
