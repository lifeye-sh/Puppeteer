package application

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
)

func TestR16NativeSSHPassword(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := sshTarget(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, e := pool.Exec(ctx, `UPDATE assets SET alias='r16-password' WHERE id=$1`, asset); e != nil {
		t.Fatal(e)
	}
	var uid string
	if e := pool.QueryRow(ctx, `SELECT id FROM users WHERE username='accept-admin'`).Scan(&uid); e != nil {
		t.Fatal(e)
	}
	if e := nativeFixtureSetting(ctx, a, true); e != nil {
		t.Fatal(e)
	}
	current := func() system.Revision {
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(disabled bool, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(current().ID), map[string]any{"set": map[string]any{system.NativePasswordDisabledKey: disabled}, "password": password}, admin)
		expect(t, r, want)
	}
	if !current().Values.Bool(system.NativePasswordDisabledKey) {
		t.Fatal("upgrade must remain key-only")
	}
	save(false, "", 422)
	if e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{system.NativePasswordDisabledKey: json.RawMessage("false")}, uid)
		return e
	}); !errors.Is(e, fault.Denied) {
		t.Fatal("proof bypass", e)
	}
	for _, node := range []string{"pw-a", "pw-b"} {
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	signer, e := a.nativeGatewayKey(ctx)
	if e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); a.nativeGatewayConnection(ctx, c, signer) }()
		}
	}()
	defer func() { cancel(); ln.Close(); <-done; wg.Wait() }()
	dial := func(methods ...ssh.AuthMethod) (*ssh.Client, error) {
		return ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "accept-admin:r16-password", Auth: methods, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 4 * time.Second})
	}
	deny := func(methods ...ssh.AuthMethod) {
		t.Helper()
		if c, e := dial(methods...); e == nil {
			c.Close()
			t.Fatal("unexpected authentication")
		}
	}
	deny(ssh.Password(testPassword))
	save(false, testPassword, 202)
	deny(ssh.Password(testPassword)) // no relaxation before both consumers ACK
	for _, node := range []string{"pw-a", "pw-b"} {
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	deny(ssh.Password("invalid"))
	var failures int
	if e := pool.QueryRow(ctx, `SELECT failures FROM login_locks WHERE username='accept-admin'`).Scan(&failures); e != nil || failures != 1 {
		t.Fatal("failed counter not committed", failures, e)
	}
	c, e := dial(ssh.Password(testPassword))
	if e != nil {
		t.Fatal(e)
	}
	s, e := c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	output, _ := s.CombinedOutput("whoami")
	c.Close()
	if !strings.Contains(string(output), "EXEC RESULT whoami") {
		t.Fatal("password exec failed", string(output))
	}
	var n int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM native_ssh_sessions n JOIN session_commands c ON c.session_id=n.session_id WHERE n.auth_method='password' AND n.key_id IS NULL AND c.command='whoami'`).Scan(&n); e != nil || n != 1 {
		t.Fatal("password command audit", n, e)
	}
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM login_locks WHERE username='accept-admin'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("success did not reset lock", n, e)
	}
	// Account lock blocks even the correct password. Only disposable fixture rows
	// are expired to continue testing other independent denial reasons.
	if _, e := pool.Exec(ctx, `INSERT INTO login_locks(username,failures,locked_until,last_failed_at) VALUES('accept-admin',5,now()+interval '5 minutes',now())`); e != nil {
		t.Fatal(e)
	}
	deny(ssh.Password(testPassword))
	if e := a.identity.Unlock(ctx, "accept-admin"); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `UPDATE users SET password_changed_at=now()-interval '10000 days' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	if e := a.db.Within(ctx, func(c context.Context) error {
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"password-expiration-period": json.RawMessage("1")}, uid)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	deny(ssh.Password(testPassword))
	if _, e := pool.Exec(ctx, `UPDATE users SET password_changed_at=now() WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	live := func() *ssh.Client {
		t.Helper()
		c, e := dial(ssh.Password(testPassword))
		if e != nil {
			t.Fatal(e)
		}
		s, e := c.NewSession()
		if e != nil {
			t.Fatal(e)
		}
		out, _ := s.StdoutPipe()
		if e = s.Shell(); e != nil {
			t.Fatal(e)
		}
		if line, e := bufio.NewReader(out).ReadString('\n'); e != nil || !strings.Contains(line, "READY") {
			t.Fatal(e, line)
		}
		return c
	}
	closed := func(c *ssh.Client) {
		t.Helper()
		defer c.Close()
		d := make(chan struct{})
		go func() { _ = c.Wait(); close(d) }()
		select {
		case <-d:
		case <-time.After(5 * time.Second):
			t.Fatal("password lease not revoked")
		}
	}
	c = live()
	save(true, testPassword, 202)
	closed(c)
	deny(ssh.Password(testPassword))
	save(false, testPassword, 202)
	for _, node := range []string{"pw-a", "pw-b"} {
		if _, e := a.system.ApplyConfiguration(ctx, node, func(system.Revision) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	c = live()
	if e := a.identity.ChangePassword(ctx, uid, identity.HashPassword(testPassword)); e != nil {
		t.Fatal(e)
	}
	closed(c)
	if _, e := pool.Exec(ctx, `UPDATE users SET force_mfa=true WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	deny(ssh.Password(testPassword))
	if _, e := pool.Exec(ctx, `UPDATE users SET force_mfa=false,status='disabled' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	deny(ssh.Password(testPassword))
	if _, e := pool.Exec(ctx, `UPDATE users SET status='enabled' WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	// A bound TOTP is mandatory, with replay protection, for password as for key.
	secret := []byte("R16 SSH password MFA fixture")
	sealed, e := secretbox.Seal(a.config.EncryptionKey, secret, uid)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO mfa_factors(user_id,secret_cipher,last_step) VALUES($1,$2,-1)`, uid, sealed); e != nil {
		t.Fatal(e)
	}
	deny(ssh.Password(testPassword))
	code := identity.TOTP(secret, time.Now().Unix()/30)
	interactive := ssh.KeyboardInteractive(func(_, _ string, questions []string, echo []bool) ([]string, error) {
		if len(questions) != 1 || len(echo) != 1 || echo[0] {
			t.Error("unsafe challenge")
		}
		return []string{code}, nil
	})
	c, e = dial(ssh.Password(testPassword), interactive)
	if e != nil {
		t.Fatal("password MFA", e)
	}
	s, e = c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	output, _ = s.CombinedOutput("hostname")
	c.Close()
	if !strings.Contains(string(output), "EXEC RESULT hostname") {
		t.Fatal("MFA exec", string(output))
	}
	deny(ssh.Password(testPassword), interactive)
	if e := a.identity.QuarantineRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if !current().Values.Bool(system.NativePasswordDisabledKey) {
		t.Fatal("restore enabled password gateway")
	}
}

func TestR16NativeSSHPasswordConfigurationMFA(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	secret, _, admin := enroll(t, srv, admin)
	v, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	save := func(code string, want int) {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{system.NativePasswordDisabledKey: false}, "password": testPassword, "totp": code}, admin)
		expect(t, r, want)
	}
	save("", 400)
	save(consumedAdminTOTP(t, a, secret), 400)
	save(freshAdminTOTP(t, a, secret), 202)
}
