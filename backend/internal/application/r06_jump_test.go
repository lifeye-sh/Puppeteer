package application

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/url"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/resource"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestR06SSHJump(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "proof.txt"), []byte("jump-sftp"), 0600); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, root)
	var forwards atomic.Int32
	var deny atomic.Bool
	jhost, jport, _ := sshTargetOptions(t, "", func(c ssh.NewChannel) {
		var req struct {
			Host       string
			Port       uint32
			Origin     string
			OriginPort uint32
		}
		if ssh.Unmarshal(c.ExtraData(), &req) != nil || req.Host != host || int(req.Port) != port || deny.Load() {
			_ = c.Reject(ssh.Prohibited, "blocked")
			return
		}
		raw, e := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if e != nil {
			_ = c.Reject(ssh.ConnectionFailed, "unavailable")
			return
		}
		ch, requests, e := c.Accept()
		if e != nil {
			raw.Close()
			return
		}
		forwards.Add(1)
		go ssh.DiscardRequests(requests)
		go func() {
			defer raw.Close()
			defer ch.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(raw, ch); _ = raw.(*net.TCPConn).CloseWrite(); close(done) }()
			_, _ = io.Copy(ch, raw)
			_ = ch.CloseWrite()
			_ = ch.Close()
			_ = raw.Close()
			<-done
		}()
	})
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	id := r04Asset(t, srv, admin, host, port, true)
	r := call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	expect(t, r, 200)
	var asset resource.Asset
	_ = json.Unmarshal(r.body, &asset)
	jump := connectivity.Jump{Name: "test jump", Host: jhost, Port: jport, CredentialID: asset.CredentialID, Enabled: true}
	r = call(t, srv.URL, "POST", "/api/admin/ssh-jumps", jump, admin, origin)
	expect(t, r, 200)
	jump.ID = object(t, r)["id"].(string)
	jump.Version = 1
	path := "/api/admin/ssh-jumps/" + jump.ID
	expect(t, call(t, srv.URL, "GET", "/api/admin/ssh-jumps", nil, nil, origin), 401)
	asset.JumpID = jump.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, asset, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	r = call(t, srv.URL, "POST", path+"/discover", nil, admin, origin)
	expect(t, r, 200)
	var observed struct {
		Observed connectivity.Trust
		Version  int64
	}
	_ = json.Unmarshal(r.body, &observed)
	trust := map[string]any{"version": observed.Version, "publicKey": observed.Observed.PublicKey}
	expect(t, call(t, srv.URL, "POST", path+"/trust", trust, admin, origin), 204)
	jump.Version++
	expect(t, call(t, srv.URL, "POST", path+"/trust", trust, admin, origin), 409)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 200)
	sid, ticket := reserve(t, srv, admin, id, false)
	ws := attach(t, srv, admin, sid, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+sid)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	r = call(t, srv.URL, "GET", "/api/sessions/"+sid+"/files/content?path="+url.QueryEscape(filepath.Join(root, "proof.txt")), nil, admin, origin)
	expect(t, r, 200)
	if string(r.body) != "jump-sftp" {
		t.Fatal(string(r.body))
	}
	if forwards.Load() < 2 {
		t.Fatal("target bypassed jump")
	}
	jump.Enabled = false
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	jump.Version++
	waitClosed(t, a, sid)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+sid+"/recording", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	jump.Enabled = true
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	jump.Version++
	deny.Store(true)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	deny.Store(false)
	// Even a reachable target must fail when the jump key is wrong; no direct fallback.
	_, _, wrong := sshTarget(t)
	expect(t, call(t, srv.URL, "POST", path+"/trust", map[string]any{"version": jump.Version, "publicKey": string(ssh.MarshalAuthorizedKey(wrong.PublicKey()))}, admin, origin), 204)
	jump.Version++
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	jump.Host = "localhost"
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	r = call(t, srv.URL, "GET", "/api/admin/ssh-jumps", nil, admin, origin)
	expect(t, r, 200)
	var rows []connectivity.Jump
	_ = json.Unmarshal(r.body, &rows)
	if len(rows) != 1 || rows[0].PublicKey != "" {
		t.Fatal("endpoint edit retained trust")
	}
}

func TestR06SSHJumpChain(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "proof.txt"), []byte("jump-sftp"), 0600); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, root)
	var forwards atomic.Int32
	var deny atomic.Bool
	jhost, jport, _ := sshTargetOptions(t, "", func(c ssh.NewChannel) {
		var req struct {
			Host       string
			Port       uint32
			Origin     string
			OriginPort uint32
		}
		if ssh.Unmarshal(c.ExtraData(), &req) != nil || req.Host != host || int(req.Port) != port || deny.Load() {
			_ = c.Reject(ssh.Prohibited, "blocked")
			return
		}
		raw, e := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if e != nil {
			_ = c.Reject(ssh.ConnectionFailed, "unavailable")
			return
		}
		ch, requests, e := c.Accept()
		if e != nil {
			raw.Close()
			return
		}
		forwards.Add(1)
		go ssh.DiscardRequests(requests)
		go func() {
			defer raw.Close()
			defer ch.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(raw, ch); _ = raw.(*net.TCPConn).CloseWrite(); close(done) }()
			_, _ = io.Copy(ch, raw)
			_ = ch.CloseWrite()
			_ = ch.Close()
			_ = raw.Close()
			<-done
		}()
	})
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	id := r04Asset(t, srv, admin, host, port, true)
	r := call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	expect(t, r, 200)
	var asset resource.Asset
	_ = json.Unmarshal(r.body, &asset)
	// Outer hop only forwards to the inner jump, proving both layers are used.
	var outerCount atomic.Int32
	outerHost, outerPort, _ := sshTargetOptions(t, "", func(c ssh.NewChannel) {
		var req struct {
			Host       string
			Port       uint32
			Origin     string
			OriginPort uint32
		}
		if ssh.Unmarshal(c.ExtraData(), &req) != nil || req.Host != jhost || int(req.Port) != jport {
			_ = c.Reject(ssh.Prohibited, "wrong hop")
			return
		}
		raw, e := net.Dial("tcp", net.JoinHostPort(jhost, strconv.Itoa(jport)))
		if e != nil {
			_ = c.Reject(ssh.ConnectionFailed, "unavailable")
			return
		}
		ch, requests, e := c.Accept()
		if e != nil {
			raw.Close()
			return
		}
		outerCount.Add(1)
		go ssh.DiscardRequests(requests)
		go func() {
			defer raw.Close()
			defer ch.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(raw, ch); _ = raw.(*net.TCPConn).CloseWrite(); close(done) }()
			_, _ = io.Copy(ch, raw)
			_ = ch.Close()
			_ = raw.Close()
			<-done
		}()
	})
	outer := connectivity.Jump{Name: "outer", Host: outerHost, Port: outerPort, CredentialID: asset.CredentialID, Enabled: true}
	r = call(t, srv.URL, "POST", "/api/admin/ssh-jumps", outer, admin, origin)
	expect(t, r, 200)
	outer.ID = object(t, r)["id"].(string)
	r = call(t, srv.URL, "POST", "/api/admin/ssh-jumps/"+outer.ID+"/discover", nil, admin, origin)
	expect(t, r, 200)
	var outerObserved struct {
		Observed connectivity.Trust
		Version  int64
	}
	_ = json.Unmarshal(r.body, &outerObserved)
	expect(t, call(t, srv.URL, "POST", "/api/admin/ssh-jumps/"+outer.ID+"/trust", map[string]any{"version": outerObserved.Version, "publicKey": outerObserved.Observed.PublicKey}, admin, origin), 204)
	outer.Version = 2
	jump := connectivity.Jump{ParentID: outer.ID, Name: "test jump", Host: jhost, Port: jport, CredentialID: asset.CredentialID, Enabled: true}
	r = call(t, srv.URL, "POST", "/api/admin/ssh-jumps", jump, admin, origin)
	expect(t, r, 200)
	jump.ID = object(t, r)["id"].(string)
	jump.Version = 1
	path := "/api/admin/ssh-jumps/" + jump.ID
	expect(t, call(t, srv.URL, "GET", "/api/admin/ssh-jumps", nil, nil, origin), 401)
	asset.JumpID = jump.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, asset, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	r = call(t, srv.URL, "POST", path+"/discover", nil, admin, origin)
	expect(t, r, 200)
	var observed struct {
		Observed connectivity.Trust
		Version  int64
	}
	_ = json.Unmarshal(r.body, &observed)
	trust := map[string]any{"version": observed.Version, "publicKey": observed.Observed.PublicKey}
	expect(t, call(t, srv.URL, "POST", path+"/trust", trust, admin, origin), 204)
	jump.Version++
	expect(t, call(t, srv.URL, "POST", path+"/trust", trust, admin, origin), 409)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 200)
	sid, ticket := reserve(t, srv, admin, id, false)
	ws := attach(t, srv, admin, sid, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+sid)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	r = call(t, srv.URL, "GET", "/api/sessions/"+sid+"/files/content?path="+url.QueryEscape(filepath.Join(root, "proof.txt")), nil, admin, origin)
	expect(t, r, 200)
	if string(r.body) != "jump-sftp" {
		t.Fatal(string(r.body))
	}
	if forwards.Load() < 2 {
		t.Fatal("target bypassed jump")
	}
	if outerCount.Load() < 3 {
		t.Fatal("outer hop bypassed")
	}
	outer.Enabled = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+outer.ID, outer, admin, origin), 200)
	outer.Version++
	waitClosed(t, a, sid)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	outer.Enabled = true
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+outer.ID, outer, admin, origin), 200)
	outer.Version++
	jump.Enabled = false
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	jump.Version++
	waitClosed(t, a, sid)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+sid+"/recording", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	jump.Enabled = true
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	jump.Version++
	deny.Store(true)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	deny.Store(false)
	// Even a reachable target must fail when the jump key is wrong; no direct fallback.
	_, _, wrong := sshTarget(t)
	expect(t, call(t, srv.URL, "POST", path+"/trust", map[string]any{"version": jump.Version, "publicKey": string(ssh.MarshalAuthorizedKey(wrong.PublicKey()))}, admin, origin), 204)
	jump.Version++
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	jump.Host = "localhost"
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	r = call(t, srv.URL, "GET", "/api/admin/ssh-jumps", nil, admin, origin)
	expect(t, r, 200)
	var rows []connectivity.Jump
	_ = json.Unmarshal(r.body, &rows)
	var found bool
	for _, row := range rows {
		if row.ID == jump.ID && row.PublicKey == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("endpoint edit retained trust")
	}
}

func TestR06GatewayGroupFailover(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "proof.txt"), []byte("jump-sftp"), 0600); e != nil {
		t.Fatal(e)
	}
	host, port, _ := sshTargetRoot(t, root)
	var forwards atomic.Int32
	var deny atomic.Bool
	jhost, jport, _ := sshTargetOptions(t, "", func(c ssh.NewChannel) {
		var req struct {
			Host       string
			Port       uint32
			Origin     string
			OriginPort uint32
		}
		if ssh.Unmarshal(c.ExtraData(), &req) != nil || req.Host != host || int(req.Port) != port || deny.Load() {
			_ = c.Reject(ssh.Prohibited, "blocked")
			return
		}
		raw, e := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if e != nil {
			_ = c.Reject(ssh.ConnectionFailed, "unavailable")
			return
		}
		ch, requests, e := c.Accept()
		if e != nil {
			raw.Close()
			return
		}
		forwards.Add(1)
		go ssh.DiscardRequests(requests)
		go func() {
			defer raw.Close()
			defer ch.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(raw, ch); _ = raw.(*net.TCPConn).CloseWrite(); close(done) }()
			_, _ = io.Copy(ch, raw)
			_ = ch.CloseWrite()
			_ = ch.Close()
			_ = raw.Close()
			<-done
		}()
	})
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	id := r04Asset(t, srv, admin, host, port, true)
	r := call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	expect(t, r, 200)
	var asset resource.Asset
	_ = json.Unmarshal(r.body, &asset)
	jump := connectivity.Jump{Name: "test jump", Host: jhost, Port: jport, CredentialID: asset.CredentialID, Enabled: true}
	r = call(t, srv.URL, "POST", "/api/admin/ssh-jumps", jump, admin, origin)
	expect(t, r, 200)
	jump.ID = object(t, r)["id"].(string)
	jump.Version = 1
	path := "/api/admin/ssh-jumps/" + jump.ID
	expect(t, call(t, srv.URL, "GET", "/api/admin/ssh-jumps", nil, nil, origin), 401)
	r = call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "offline", "networkScope": connectivity.NetworkScope{CIDRs: []string{"127.0.0.1/32"}, Ports: []int{port}}}, admin, origin)
	expect(t, r, 201)
	r = call(t, srv.URL, "POST", "/api/agent/v1/enroll", connectivity.Enrollment{RegistrationToken: object(t, r)["token"].(string), Name: "offline", OS: "linux", Arch: "amd64", Version: "test"}, nil, "")
	expect(t, r, 201)
	group := connectivity.GatewayGroup{Name: "failover", Enabled: true, Members: []connectivity.GatewayMember{{Kind: "agent", ID: object(t, r)["agentId"].(string)}, {Kind: "jump", ID: jump.ID}}}
	r = call(t, srv.URL, "POST", "/api/admin/gateway-groups", group, admin, origin)
	expect(t, r, 200)
	group.ID = object(t, r)["id"].(string)
	group.Version = 1
	asset.GatewayGroupID = group.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, asset, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	r = call(t, srv.URL, "POST", path+"/discover", nil, admin, origin)
	expect(t, r, 200)
	var observed struct {
		Observed connectivity.Trust
		Version  int64
	}
	_ = json.Unmarshal(r.body, &observed)
	trust := map[string]any{"version": observed.Version, "publicKey": observed.Observed.PublicKey}
	expect(t, call(t, srv.URL, "POST", path+"/trust", trust, admin, origin), 204)
	jump.Version++
	expect(t, call(t, srv.URL, "POST", path+"/trust", trust, admin, origin), 409)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 200)
	sid, ticket := reserve(t, srv, admin, id, false)
	ws := attach(t, srv, admin, sid, ticket)
	defer ws.Close()
	readUntil(t, ws, "3"+sid)
	_ = ws.WriteMessage(websocket.TextMessage, []byte("1whoami\n"))
	readUntil(t, ws, "EXECUTED whoami")
	r = call(t, srv.URL, "GET", "/api/sessions/"+sid+"/files/content?path="+url.QueryEscape(filepath.Join(root, "proof.txt")), nil, admin, origin)
	expect(t, r, 200)
	if string(r.body) != "jump-sftp" {
		t.Fatal(string(r.body))
	}
	if forwards.Load() < 2 {
		t.Fatal("target bypassed jump")
	}
	group.Enabled = false
	expect(t, call(t, srv.URL, "PUT", "/api/admin/gateway-groups/"+group.ID, group, admin, origin), 200)
	waitClosed(t, a, sid)
	group.Version++
	group.Enabled = true
	expect(t, call(t, srv.URL, "PUT", "/api/admin/gateway-groups/"+group.ID, group, admin, origin), 200)
	jump.Enabled = false
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	jump.Version++
	waitClosed(t, a, sid)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+sid+"/recording", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	jump.Enabled = true
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	jump.Version++
	deny.Store(true)
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	deny.Store(false)
	// Even a reachable target must fail when the jump key is wrong; no direct fallback.
	_, _, wrong := sshTarget(t)
	expect(t, call(t, srv.URL, "POST", path+"/trust", map[string]any{"version": jump.Version, "publicKey": string(ssh.MarshalAuthorizedKey(wrong.PublicKey()))}, admin, origin), 204)
	jump.Version++
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets/"+id+"/host-key/discover", nil, admin, origin), 422)
	jump.Host = "localhost"
	expect(t, call(t, srv.URL, "PUT", path, jump, admin, origin), 200)
	r = call(t, srv.URL, "GET", "/api/admin/ssh-jumps", nil, admin, origin)
	expect(t, r, 200)
	var rows []connectivity.Jump
	_ = json.Unmarshal(r.body, &rows)
	if len(rows) != 1 || rows[0].PublicKey != "" {
		t.Fatal("endpoint edit retained trust")
	}
}
