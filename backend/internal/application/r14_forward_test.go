package application

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestR14NativeForward(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	if _, e := pool.Exec(ctx, `UPDATE assets SET alias='forward-target' WHERE id=$1`, asset); e != nil {
		t.Fatal(e)
	}
	if e := nativeFixtureSetting(ctx, a, true); e != nil {
		t.Fatal(e)
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewSignerFromKey(private)
	var keyID string
	e := a.db.Within(ctx, func(ctx context.Context) error {
		u, e := a.identity.Current(ctx, admin.Value)
		if e != nil {
			return e
		}
		keyID, _, e = a.identity.CreatePersonalCredential(ctx, u.ID, "ssh", "forward-test", string(ssh.MarshalAuthorizedKey(key.PublicKey())), nil, 1)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	signer, e := a.nativeGatewayKey(ctx)
	if e != nil {
		t.Fatal(e)
	}
	gateway, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	echo, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	_, targetPort, _ := net.SplitHostPort(echo.Addr().String())
	targetNumber, _ := strconv.Atoi(targetPort)
	var wg sync.WaitGroup
	accepts := make(chan struct{})
	echoAccepts := make(chan struct{})
	go func() {
		defer close(accepts)
		for {
			raw, e := gateway.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); a.nativeGatewayConnection(ctx, raw, signer) }()
		}
	}()
	go func() {
		defer close(echoAccepts)
		for {
			raw, e := echo.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				stop := context.AfterFunc(ctx, func() { raw.Close() })
				defer stop()
				scanner := bufio.NewScanner(raw)
				for scanner.Scan() {
					if _, e := io.WriteString(raw, "echo:"+scanner.Text()+"\n"); e != nil {
						return
					}
				}
				_, _ = io.WriteString(raw, "EOF\n")
			}()
		}
	}()
	defer func() {
		cancel()
		gateway.Close()
		echo.Close()
		<-accepts
		<-echoAccepts
		wg.Wait()
	}()
	dial := func() *ssh.Client {
		c, e := ssh.Dial("tcp", gateway.Addr().String(), &ssh.ClientConfig{User: "accept-admin:forward-target", Auth: []ssh.AuthMethod{ssh.PublicKeys(key)}, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 3 * time.Second})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	open := func(c *ssh.Client, hostname string, port int) (ssh.Channel, error) {
		p := struct {
			Host       string
			Port       uint32
			Origin     string
			OriginPort uint32
		}{hostname, uint32(port), "spoofed-origin", 1234}
		ch, req, e := c.OpenChannel("direct-tcpip", ssh.Marshal(p))
		if e == nil {
			go ssh.DiscardRequests(req)
		}
		return ch, e
	}
	c := dial()
	defer c.Close()
	if ch, e := open(c, "127.0.0.1", targetNumber); e == nil {
		ch.Close()
		t.Fatal("default forwarding allowed")
	}
	current := func() map[string]any {
		r := call(t, srv.URL, "GET", "/api/admin/native-forward", nil, admin, origin)
		expect(t, r, 200)
		return object(t, r)
	}
	save := func(enabled bool, rules []map[string]any, version any) result {
		return call(t, srv.URL, "PUT", "/api/admin/native-forward", map[string]any{"password": testPassword, "enabled": enabled, "version": version, "rules": rules}, admin, origin)
	}
	rule := map[string]any{"assetId": asset, "host": "localhost", "port": targetNumber, "addresses": []string{"169.254.169.254"}}
	rules := []map[string]any{rule}
	initial := current()
	expect(t, save(true, nil, initial["version"]), 422)
	expect(t, save(true, rules, initial["version"]), 200)
	expect(t, save(true, rules, initial["version"]), 409)
	pinned := current()
	// Disabling must never depend on resolving stale/deleted targets.
	stale := []map[string]any{{"assetId": "11111111-1111-4111-8111-111111111111", "host": "unresolvable.invalid", "port": 443}}
	expect(t, save(false, stale, pinned["version"]), 200)
	expect(t, save(true, rules, current()["version"]), 200)
	pinned = current()
	if strings.Contains(string(call(t, srv.URL, "GET", "/api/admin/native-forward", nil, admin, origin).body), "169.254.169.254") {
		t.Fatal("client-supplied DNS pins accepted")
	}
	if ch, e := open(c, "127.0.0.1", targetNumber); e == nil {
		ch.Close()
		t.Fatal("alias not explicitly permitted")
	}
	if ch, e := open(c, "localhost", targetNumber+1); e == nil {
		ch.Close()
		t.Fatal("unlisted port permitted")
	}
	if listener, e := c.Listen("tcp", "127.0.0.1:0"); e == nil {
		listener.Close()
		t.Fatal("remote listen permitted")
	}
	if sh, e := c.NewSession(); e == nil {
		sh.Close()
		t.Fatal("terminal mixed with forwarding")
	}
	ch, e := open(c, "localhost", targetNumber)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.WriteString(ch, "roundtrip\n"); e != nil {
		t.Fatal(e)
	}
	reader := bufio.NewReader(ch)
	line, e := reader.ReadString('\n')
	if e != nil || line != "echo:roundtrip\n" {
		t.Fatal("roundtrip", line, e)
	}
	if e = ch.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	rest, e := io.ReadAll(reader)
	if e != nil || string(rest) != "EOF\n" {
		t.Fatal("half-close", string(rest), e)
	}
	ch.Close()
	limited := dial()
	channels := []ssh.Channel{}
	for i := 0; i < 8; i++ {
		stream, err := open(limited, "localhost", targetNumber)
		if err != nil {
			t.Fatal("within stream quota", i, err)
		}
		channels = append(channels, stream)
	}
	if extra, err := open(limited, "localhost", targetNumber); err == nil {
		extra.Close()
		t.Fatal("ninth stream accepted")
	}
	for _, stream := range channels {
		stream.Close()
	}
	limited.Close()
	// Withhold reads beyond SSH's receive window: the entire stalled transport
	// must close even when the peer never acknowledges a channel-close request.
	stalled := dial()
	defer stalled.Close()
	blocked, err := open(stalled, "localhost", targetNumber)
	if err != nil {
		t.Fatal(err)
	}
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = io.WriteString(blocked, strings.Repeat(strings.Repeat("x", 8192)+"\n", 512))
	}()
	transportDone := make(chan error, 1)
	stallStart := time.Now()
	go func() { transportDone <- stalled.Wait() }()
	select {
	case <-transportDone:
	case <-time.After(23 * time.Second):
		t.Fatal("stalled SSH window retained the transport")
	}
	<-written
	if time.Since(stallStart) < 14*time.Second {
		t.Fatal("transport ended before write timeout")
	}
	policy := postID(t, srv, "/api/admin/policies/strategy", map[string]any{"name": "no opaque uploads", "enabled": true, "global": true, "actions": map[string]bool{"upload": false}}, admin)
	if stream, err := open(c, "localhost", targetNumber); err == nil {
		stream.Close()
		t.Fatal("upload restriction bypassed")
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/policies/strategy/"+policy+"?version=1", nil, admin, origin), 204)
	// Each active channel must close after a settings version change.
	live, e := open(c, "localhost", targetNumber)
	if e != nil {
		t.Fatal(e)
	}
	expect(t, save(false, rules, pinned["version"]), 200)
	done := make(chan error, 1)
	go func() { _, e := live.Read(make([]byte, 1)); done <- e }()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("revoked stream remained usable")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("settings revocation did not close stream")
	}
	live.Close()
	if ch, e := open(c, "localhost", targetNumber); e == nil {
		ch.Close()
		t.Fatal("disabled forwarding reopened")
	}
	expect(t, save(true, rules, current()["version"]), 200)
	// Configuration changes may close the old transport. Reconnect as a real
	// client must, and independently verify key revocation on the new lease.
	c.Close()
	revokedClient := dial()
	defer revokedClient.Close()
	live, e = open(revokedClient, "localhost", targetNumber)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE personal_ssh_keys SET revoked_at=now() WHERE id=$1`, keyID); e != nil {
		t.Fatal(e)
	}
	done = make(chan error, 1)
	go func() { _, e := live.Read(make([]byte, 1)); done <- e }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("key revocation did not close stream")
	}
	live.Close()
	c.Close()
	var id string
	var state string
	var sent, received int64
	for i := 0; i < 50; i++ {
		e = pool.QueryRow(ctx, `SELECT session_id,state,bytes_to_target,bytes_from_target FROM native_forward_streams WHERE state='closed' AND bytes_to_target=10 LIMIT 1`).Scan(&id, &state, &sent, &received)
		if e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e != nil || sent != 10 || received != 19 {
		t.Fatal("forward byte audit", sent, received, e)
	}
	r := call(t, srv.URL, "GET", "/api/sessions/"+id+"/forward-streams", nil, admin, origin)
	expect(t, r, 200)
	if object(t, r)["total"].(float64) != 2 {
		t.Fatal("forward audit count", string(r.body))
	}
	postID(t, srv, "/api/admin/users", userInput("forward-other", nil, nil), admin)
	other := loginAs(t, srv, "forward-other")
	expect(t, call(t, srv.URL, "GET", "/api/admin/native-forward", nil, other, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/forward-streams", nil, other, origin), 403)
	var forwarding bool
	if e = pool.QueryRow(ctx, `SELECT native_forward FROM sessions WHERE id=$1`, id).Scan(&forwarding); e != nil || !forwarding {
		t.Fatal("missing forwarding mode", e)
	}
}

func TestR14ForwardAddressBounds(t *testing.T) {
	for _, s := range []string{"0.0.0.0", "::", "169.254.169.254", "fe80::1", "224.0.0.1", "::ffff:224.0.0.1", "fe80::1%eth0", "not-an-ip"} {
		if forwardAddress(s) {
			t.Fatal("unsafe address accepted", s)
		}
	}
	for _, s := range []string{"127.0.0.1", "::1", "10.2.3.4", "2001:db8::1"} {
		if !forwardAddress(s) {
			t.Fatal("explicit address rejected", s)
		}
	}
}
