package application

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/url"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestR06SSHAndSFTPOverAgent(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "agent-proof.txt"), []byte("through agent"), 0600); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, root)
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	r := call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "dial", "networkScope": map[string]any{"cidrs": []string{host + "/32"}, "ports": []int{port}}}, admin, origin)
	expect(t, r, 201)
	r = call(t, srv.URL, "POST", "/api/agent/v1/enroll", connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "dial", OS: "linux", Arch: "amd64", Version: "test"}, nil, "")
	expect(t, r, 201)
	cfg := agent.Config{Endpoint: srv.URL, AgentID: object(t, r)["agentId"].(string), DeviceKey: object(t, r)["deviceKey"].(string), AllowInsecureDevelopment: true}
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- agent.RunConnection(life, cfg, func(int64) { close(ready) }) }()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(8 * time.Second):
		t.Fatal("agent not ready")
	}
	id := r04Asset(t, srv, admin, host, port, true)
	r = call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	expect(t, r, 200)
	var asset resource.Asset
	_ = json.Unmarshal(r.body, &asset)
	asset.AgentID = cfg.AgentID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, asset, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 200)
	session, ticket := reserve(t, srv, admin, id, false)
	ws := attach(t, srv, admin, session, ticket)
	readUntil(t, ws, "3"+session)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	r = call(t, srv.URL, "GET", "/api/sessions/"+session+"/files/content?path="+url.QueryEscape(filepath.Join(root, "agent-proof.txt")), nil, admin, origin)
	expect(t, r, 200)
	if string(r.body) != "through agent" {
		t.Fatal(string(r.body))
	}
	// End-to-end use survives multiple lease renewals and then closes on revocation.
	time.Sleep(4 * time.Second)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1pwd\n"))
	readUntil(t, ws, "EXECUTED pwd")
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+cfg.AgentID+"/revoke", nil, admin, origin), 204)
	waitClosed(t, a, session)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+session+"/recording", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("agent stayed online after revoke")
	}
}
