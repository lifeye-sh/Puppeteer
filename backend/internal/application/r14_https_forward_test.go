package application

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/nativeclient"
	"strings"
	"testing"
	"time"
)

func TestR14HTTPSForward(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, server := r02Server(t, false)
	browser := loginAs(t, server, "accept-admin")
	asset := r04Asset(t, server, browser, host, port, true)
	tlsServer := httptest.NewTLSServer(server.Config.Handler)
	defer tlsServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	token := nativeclient.Token{Access: nativeFixtureToken(t, a, browser, []string{"assets:read", "sessions:connect"})}
	client, e := nativeclient.New(tlsServer.URL, tlsServer.Client().Transport)
	if e != nil {
		t.Fatal(e)
	}
	if peer, e := client.ConnectForward(ctx, token, asset); e == nil {
		peer.Close()
		t.Fatal("disabled forwarding reserved")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			_, _ = io.WriteString(w, "holding")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "fixture:"+r.URL.Path)
	}))
	defer target.Close()
	address := strings.TrimPrefix(target.URL, "http://")
	targetHost, targetPort, _ := net.SplitHostPort(address)
	var targetNumber int
	_, _ = fmt.Sscan(targetPort, &targetNumber)
	rules, _ := json.Marshal([]map[string]any{{"assetId": asset, "host": targetHost, "port": targetNumber, "addresses": []string{targetHost}}})
	if _, e = pool.Exec(ctx, `UPDATE native_forward_settings SET enabled=true,version=version+1,rules=$1`, rules); e != nil {
		t.Fatal(e)
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	request := func(entry string, readonly bool) result {
		body, _ := json.Marshal(map[string]any{"assetId": asset, "entry": entry, "readonly": readonly, "idempotencyKey": "https-forward-mode-boundary", "clientPublicKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))})
		req, _ := http.NewRequestWithContext(ctx, "POST", tlsServer.URL+"/api/native/v1/connect", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token.Access)
		req.Header.Set("Content-Type", "application/json")
		res, err := tlsServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: b}
	}
	expect(t, request("ssh-forward", true), 403)
	expect(t, request("ssh", false), 200)
	expect(t, request("ssh-forward", false), 409)
	ordinary, e := client.ConnectSSH(ctx, token, asset)
	if e != nil {
		t.Fatal(e)
	}
	if c, e := ordinary.Dial("tcp", address); e == nil {
		c.Close()
		t.Fatal("ordinary SSH switched to forwarding")
	}
	ordinary.Close()
	peer, e := client.ConnectForward(ctx, token, asset)
	if e != nil {
		t.Fatal(e)
	}
	defer peer.Close()
	if shell, e := peer.NewSession(); e == nil {
		shell.Close()
		t.Fatal("forwarding opened shell")
	}
	if remote, e := peer.Listen("tcp", "127.0.0.1:0"); e == nil {
		remote.Close()
		t.Fatal("remote listener allowed")
	}
	if c, e := peer.Dial("tcp", net.JoinHostPort(targetHost, "1")); e == nil {
		c.Close()
		t.Fatal("unlisted target allowed")
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	finished := make(chan error, 1)
	go func() { finished <- nativeclient.ServeForward(ctx, peer, listener, address) }()
	localHTTP := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 4 * time.Second}
	response, e := localHTTP.Get("http://" + listener.Addr().String() + "/roundtrip")
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || string(body) != "fixture:/roundtrip" {
		t.Fatal("HTTPS relay roundtrip", string(body), e)
	}
	// The production middleware must treat this as a leased long connection,
	// not a normal API request with a 15-second deadline.
	select {
	case <-time.After(16 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	again, err := localHTTP.Get("http://" + listener.Addr().String() + "/after-idle")
	if err != nil {
		t.Fatal("bridge did not survive normal API deadline", err)
	}
	idleBody, err := io.ReadAll(again.Body)
	again.Body.Close()
	if err != nil || string(idleBody) != "fixture:/after-idle" {
		t.Fatal("idle roundtrip", err)
	}
	held, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	_ = held.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = fmt.Fprint(held, "GET /hold HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n")
	reader := bufio.NewReader(held)
	line, e := reader.ReadString('\n')
	if e != nil || !strings.Contains(line, "200") {
		t.Fatal(line, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE native_forward_settings SET enabled=false,version=version+1`); e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadAll(reader); e != nil {
		t.Fatal("disable did not close stream", e)
	}
	held.Close()
	if _, e = pool.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now()`); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-finished:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked family retained local listener")
	}
	var count int
	for i := 0; i < 30; i++ {
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM native_forward_streams f JOIN sessions s ON s.id=f.session_id WHERE s.native_forward AND f.ended_at IS NOT NULL AND f.bytes_to_target>0 AND f.bytes_from_target>0`).Scan(&count)
		if count >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if count != 3 {
		t.Fatal("missing forwarding byte audit", count)
	}
	if c, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second); e == nil {
		c.Close()
		t.Fatal("listener remained open")
	}
}
