package application

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/ssh"
	"net"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestR14NativeSSHGateway(t *testing.T) {
	host, port, _ := sshTarget(t)
	pool, a, srv := r02Server(t, false)
	browser := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, browser, host, port, true)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	var alias, userID, keyID string
	if _, e := pool.Exec(ctx, `UPDATE assets SET alias='r14-gateway-target' WHERE id=$1`, asset); e != nil {
		t.Fatal(e)
	}
	if e := pool.QueryRow(ctx, `SELECT alias FROM assets WHERE id=$1`, asset).Scan(&alias); e != nil {
		t.Fatal(e)
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewSignerFromKey(private)
	e := a.db.Within(ctx, func(ctx context.Context) error {
		if e := nativeFixtureSetting(ctx, a, true); e != nil {
			return e
		}
		u, e := a.identity.Current(ctx, browser.Value)
		if e != nil {
			return e
		}
		userID = u.ID
		keyID, _, e = a.identity.CreatePersonalCredential(ctx, u.ID, "ssh", "gateway-test", string(ssh.MarshalAuthorizedKey(key.PublicKey())), nil, 7)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	signer, e := a.nativeGatewayKey(ctx)
	if e != nil {
		t.Fatal(e)
	}
	again, e := a.nativeGatewayKey(ctx)
	if e != nil || string(again.PublicKey().Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Fatal("host key not stable", e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); a.nativeGatewayConnection(ctx, raw, signer) }()
		}
	}()
	defer func() { cancel(); listener.Close(); <-acceptDone; wg.Wait() }()
	dial := func(methods ...ssh.AuthMethod) (*ssh.Client, error) {
		return ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "accept-admin:" + alias, Auth: methods, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 4 * time.Second})
	}
	_, wrongPrivate, _ := ed25519.GenerateKey(rand.Reader)
	wrongKey, _ := ssh.NewSignerFromKey(wrongPrivate)
	if c, e := dial(ssh.PublicKeys(wrongKey)); e == nil {
		c.Close()
		t.Fatal("unknown key accepted")
	}
	c, e := dial(ssh.PublicKeys(key))
	if e != nil {
		t.Fatal(e)
	}
	session, e := c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	out, e := session.CombinedOutput("whoami")
	var status *ssh.ExitError
	if !strings.Contains(string(out), "EXEC RESULT whoami") || !errors.As(e, &status) || status.ExitStatus() != 7 {
		t.Fatal("gateway exec", string(out), e)
	}
	c.Close()
	var n int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM session_commands c JOIN native_ssh_sessions n ON n.session_id=c.session_id WHERE n.key_id=$1 AND c.command='whoami'`, keyID).Scan(&n); e != nil || n != 1 {
		t.Fatal("native exec audit", n, e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM login_logs l JOIN native_ssh_sessions n ON n.session_id::text=l.request_id WHERE n.key_id=$1 AND l.outcome='succeeded'`, keyID).Scan(&n); e != nil || n != 1 {
		t.Fatal("native login audit", n, e)
	}
	accessPolicy := func(enabled bool) {
		t.Helper()
		e := a.db.Within(ctx, func(c context.Context) error {
			value, _ := json.Marshal(enabled)
			_, err := a.system.SaveConfiguration(system.WithAccessMFAConfigurationProof(c), nil, map[string]json.RawMessage{"access-require-mfa": value}, "")
			return err
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	accessPolicy(true)
	if peer, err := dial(ssh.PublicKeys(key)); err == nil {
		peer.Close()
		t.Fatal("global access MFA bypass without factor")
	}
	// A forced MFA account without a bound factor may not fall back to a key.
	if _, e = pool.Exec(ctx, `UPDATE users SET force_mfa=true WHERE id=$1`, userID); e != nil {
		t.Fatal(e)
	}
	if c, e := dial(ssh.PublicKeys(key)); e == nil {
		c.Close()
		t.Fatal("forced MFA bypass")
	}
	secret := []byte("R14-test-TOTP-secret-only")
	sealed, e := secretbox.Seal(a.config.EncryptionKey, secret, userID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO mfa_factors(user_id,secret_cipher,last_step) VALUES($1,$2,-1) ON CONFLICT(user_id) DO UPDATE SET secret_cipher=$2,last_step=-1`, userID, sealed); e != nil {
		t.Fatal(e)
	}
	if c, e := dial(ssh.PublicKeys(key)); e == nil {
		c.Close()
		t.Fatal("bound MFA bypass")
	}
	code := identity.TOTP(secret, time.Now().Unix()/30)
	interactive := ssh.KeyboardInteractive(func(_, _ string, questions []string, echo []bool) ([]string, error) {
		if len(questions) != 1 || len(echo) != 1 || echo[0] {
			t.Error("unsafe TOTP prompt")
		}
		return []string{code}, nil
	})
	c, e = dial(ssh.PublicKeys(key), interactive)
	if e != nil {
		t.Fatal("key plus MFA", e)
	}
	mfaSession, e := c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	out, e = mfaSession.CombinedOutput("hostname")
	if !strings.Contains(string(out), "EXEC RESULT hostname") {
		t.Fatal("MFA session", e)
	}
	c.Close()
	if c, e := dial(ssh.PublicKeys(key), interactive); e == nil {
		c.Close()
		t.Fatal("TOTP replay accepted")
	}
	accessPolicy(false)
	// Restore only the isolated fixture's factor to exercise plain-key live leases.
	if _, e = pool.Exec(ctx, `DELETE FROM mfa_factors WHERE user_id=$1`, userID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE users SET force_mfa=false WHERE id=$1`, userID); e != nil {
		t.Fatal(e)
	}
	c, e = dial(ssh.PublicKeys(key))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	live, e := c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	stdout, _ := live.StdoutPipe()
	if e = live.Shell(); e != nil {
		t.Fatal(e)
	}
	if line, e := bufio.NewReader(stdout).ReadString('\n'); e != nil || !strings.Contains(line, "READY") {
		t.Fatal("live shell", e)
	}
	if forwarded, e := c.Dial("tcp", "127.0.0.1:22"); e == nil {
		forwarded.Close()
		t.Fatal("forward bypass")
	}
	if _, e = pool.Exec(ctx, `UPDATE personal_ssh_keys SET revoked_at=now() WHERE id=$1`, keyID); e != nil {
		t.Fatal(e)
	}
	closed := make(chan struct{})
	go func() { _ = c.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked SSH key did not close session")
	}
	if c, e := dial(ssh.PublicKeys(key)); e == nil {
		c.Close()
		t.Fatal("revoked key accepted")
	}
	if _, e = pool.Exec(ctx, `UPDATE personal_ssh_keys SET revoked_at=NULL,expires_at=now()-interval '1 second' WHERE id=$1`, keyID); e != nil {
		t.Fatal(e)
	}
	if c, e := dial(ssh.PublicKeys(key)); e == nil {
		c.Close()
		t.Fatal("expired key accepted")
	}
	if _, e = pool.Exec(ctx, `UPDATE personal_ssh_keys SET expires_at=now()+interval '1 hour' WHERE id=$1`, keyID); e != nil {
		t.Fatal(e)
	}
	if e = nativeFixtureSetting(ctx, a, false); e != nil {
		t.Fatal(e)
	}
	if c, e := dial(ssh.PublicKeys(key)); e == nil {
		c.Close()
		t.Fatal("disabled gateway accepted")
	}
	if e = nativeFixtureSetting(ctx, a, true); e != nil {
		t.Fatal(e)
	}
	c, e = dial(ssh.PublicKeys(key))
	if e != nil {
		t.Fatal(e)
	}
	abrupt, e := c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	stream, _ := abrupt.StdoutPipe()
	if e = abrupt.RequestPty("", 0, 0, ssh.TerminalModes{}); e != nil {
		t.Fatal("OpenSSH unspecified PTY size", e)
	}
	if e = abrupt.Shell(); e != nil {
		t.Fatal(e)
	}
	if _, e = bufio.NewReader(stream).ReadString('\n'); e != nil {
		t.Fatal(e)
	}
	c.Close()
	for attempt := 0; attempt < 50; attempt++ {
		if e = pool.QueryRow(ctx, `SELECT count(*) FROM sessions s JOIN native_ssh_sessions n ON n.session_id=s.id WHERE n.key_id=$1 AND s.closed_at IS NULL`, keyID).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n != 0 {
		t.Fatal("disconnected client left a live target session")
	}
	otherID := postID(t, srv, "/api/admin/users", userInput("ssh-no-grant", nil, nil), browser)
	e = a.db.Within(ctx, func(ctx context.Context) error {
		_, _, e := a.identity.CreatePersonalCredential(ctx, otherID, "ssh", "ungranted-key", string(ssh.MarshalAuthorizedKey(key.PublicKey())), nil, 7)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	other, e := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "ssh-no-grant:" + alias, Auth: []ssh.AuthMethod{ssh.PublicKeys(key)}, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 4 * time.Second})
	if e == nil {
		defer other.Close()
		if s, e := other.NewSession(); e == nil {
			s.Close()
			t.Fatal("registered key bypassed asset authorization")
		}
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE actor_id=$1`, otherID).Scan(&n); e != nil || n != 0 {
		t.Fatal("unauthorized user created target session", n, e)
	}
}
