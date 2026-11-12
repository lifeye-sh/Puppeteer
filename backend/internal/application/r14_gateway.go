package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
	"log/slog"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (a *App) nativeSessionUser(ctx context.Context, v session.Session, auth string) (*identity.User, error) {
	if !strings.HasPrefix(auth, "ssh:") {
		return a.identity.NativeFamilyUser(ctx, auth)
	}
	if auth != "ssh:"+v.ID {
		return nil, fault.Denied
	}
	var username, key, keyID, digest, method string
	var version, epoch, serverVersion int64
	var verified bool
	e := a.db.Row(ctx, `SELECT u.username,coalesce(k.public_key,''),coalesce(n.key_id::text,''),n.user_version,n.auth_epoch,n.server_version,n.mfa_digest,n.mfa_verified,n.auth_method FROM native_ssh_sessions n JOIN sessions s ON s.id=n.session_id JOIN users u ON u.id=s.actor_id LEFT JOIN personal_ssh_keys k ON k.id=n.key_id AND k.user_id=u.id WHERE n.session_id=$1 AND u.id=$2`, v.ID, v.ActorID).Scan(&username, &key, &keyID, &version, &epoch, &serverVersion, &digest, &verified, &method)
	if e != nil {
		return nil, fault.Denied
	}
	var p identity.NativeSSHProof
	if method == "password" {
		p, e = a.identity.NativeSSHPasswordProof(ctx, username)
	} else if method == "key" {
		p, e = a.identity.NativeSSHKey(ctx, username, key)
	} else {
		return nil, fault.Denied
	}
	if e != nil {
		return nil, e
	}
	settings, e := a.identity.OAuthSettings(ctx)
	if e != nil {
		return nil, e
	}
	if !settings.NativeEnabled || settings.Version != serverVersion || p.KeyID != keyID || p.Version != version || p.Epoch != epoch || p.MFADigest != digest || (p.NeedsMFA && !verified) {
		return nil, fault.Denied
	}
	return &p.User, nil
}

func (a *App) nativeGatewayKey(ctx context.Context) (ssh.Signer, error) {
	var sealed []byte
	e := a.db.Within(ctx, func(ctx context.Context) error {
		if _, e := a.db.Exec(ctx, `SELECT pg_advisory_xact_lock(9140051)`); e != nil {
			return e
		}
		e := a.db.Row(ctx, `SELECT private_key FROM native_ssh_gateway_keys WHERE singleton=1`).Scan(&sealed)
		if e == nil {
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		defer clear(key)
		signer, e := ssh.NewSignerFromKey(key)
		if e != nil {
			return e
		}
		sealed, e = secretbox.Seal(a.config.EncryptionKey, key, "native-ssh-gateway-host-v1")
		if e != nil {
			return e
		}
		_, e = a.db.Exec(ctx, `INSERT INTO native_ssh_gateway_keys(singleton,private_key,public_key) VALUES(1,$1,$2)`, sealed, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))))
		return e
	})
	if e != nil {
		return nil, e
	}
	key, e := secretbox.Open(a.config.EncryptionKey, sealed, "native-ssh-gateway-host-v1")
	if e != nil {
		return nil, e
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fault.Invalid
	}
	return ssh.NewSignerFromKey(ed25519.PrivateKey(key))
}

func (a *App) nativeSSHInfo(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.authenticated(r); e != nil {
		return e
	}
	settings, e := a.identity.OAuthSettings(r.Context())
	if e != nil {
		return e
	}
	public, e := a.effectiveGatewayPublicKey(r.Context())
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	fingerprint := ""
	if public != "" {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(public))
		if err != nil {
			return err
		}
		fingerprint = ssh.FingerprintSHA256(key)
	}
	_, port, _ := net.SplitHostPort(a.effectiveListenerAddress("ssh"))
	number, _ := strconv.Atoi(port)
	forward, e := a.forwardSettings(r.Context())
	if e != nil {
		return e
	}
	configuration, e := a.system.SecurityConfiguration(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"passwordAuthentication": !configuration.Values.Bool(system.NativePasswordDisabledKey) && !configuration.Values.Bool("disable-password-login"), "enabled": settings.NativeEnabled && a.effectiveListenerAddress("ssh") != "", "ready": a.nativeSSHReady.Load(), "host": a.config.NativeSSHName, "port": number, "publicKey": public, "fingerprint": fingerprint, "usernameFormat": "platformUser:assetAlias", "mfa": "TOTP required when enabled or enforced", "forwarding": settings.NativeEnabled && forward.Enabled, "forwardingSupported": true, "sftp": false})
	return nil
}

func (a *App) RunNativeSSHGateway(ctx context.Context) {
	if a.config.NativeSSHAddr == "" {
		return
	}
	for ctx.Err() == nil {
		e := a.serveNativeGateway(ctx)
		a.nativeSSHReady.Store(false)
		if ctx.Err() != nil {
			return
		}
		slog.Error("native SSH gateway stopped", "error", e)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (a *App) serveNativeGateway(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	signer, e := a.nativeGatewayKey(ctx)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp", a.config.NativeSSHAddr)
	if e != nil {
		return e
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	a.nativeSSHReady.Store(true)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	slots := make(chan struct{}, 64)
	for {
		conn, e := listener.Accept()
		if e != nil {
			return e
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-slots }(); a.nativeGatewayConnection(ctx, conn, signer) }()
	}
}

func (a *App) nativeGatewayConnection(parent context.Context, raw net.Conn, signer ssh.Signer) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer raw.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer closeOnCancel()
	if a.maintenance.Load() {
		return
	}
	ip, _, e := net.SplitHostPort(raw.RemoteAddr().String())
	if e != nil {
		return
	}
	if !a.allowAttempt("native-ssh-ip:" + ip) {
		return
	}
	_ = raw.SetDeadline(time.Now().Add(60 * time.Second))
	var proof identity.NativeSSHProof
	var canonical, alias, authMethod string
	verified := false
	validate := func(ctx context.Context, username, key string) (p identity.NativeSSHProof, e error) {
		settings, e := a.identity.OAuthSettings(ctx)
		if e != nil || !settings.NativeEnabled || a.maintenance.Load() {
			return p, fault.Proof
		}
		if authMethod == "password" {
			return a.identity.NativeSSHPasswordProof(ctx, username)
		}
		return a.identity.NativeSSHKey(ctx, username, key)
	}
	cfg := &ssh.ServerConfig{ServerVersion: "SSH-2.0-NextTerminal", MaxAuthTries: 3, PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512}}
	cfg.AddHostKey(signer)
	// Authentication mode is bound to the primary proof and cannot be changed
	// during the TOTP partial-success exchange.
	parseUser := func(meta ssh.ConnMetadata) (string, string, error) {
		if len(meta.User()) > 160 || strings.Count(meta.User(), ":") != 1 {
			return "", "", fault.Proof
		}
		parts := strings.SplitN(meta.User(), ":", 2)
		if parts[0] == "" || len(parts[0]) > 64 || parts[1] == "" || len(parts[1]) > 64 {
			return "", "", fault.Proof
		}
		if !a.allowAttempt("native-ssh-user:" + parts[0] + ":" + ip) {
			return "", "", fault.Proof
		}
		return parts[0], parts[1], nil
	}
	completePrimary := func(meta ssh.ConnMetadata, p identity.NativeSSHProof, public, target, method string) (*ssh.Permissions, error) {
		proof, canonical, alias, authMethod, verified = p, public, target, method, false
		permissions := &ssh.Permissions{Extensions: map[string]string{"native-key": p.KeyID, "native-method": method}}
		if !proof.NeedsMFA {
			return permissions, nil
		}
		expected := proof
		return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{KeyboardInteractiveCallback: func(meta ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			if meta.User() != expected.User.Username+":"+alias || !a.allowAttempt("native-ssh-mfa:"+expected.User.ID+":"+ip) {
				return nil, fault.Proof
			}
			answers, e := challenge(meta.User(), "Puppeteer verification", []string{"TOTP code: "}, []bool{false})
			if e != nil || len(answers) != 1 || len(answers[0]) != 6 {
				return nil, fault.Proof
			}
			check, stop := context.WithTimeout(ctx, 3*time.Second)
			defer stop()
			e = a.db.Within(check, func(ctx context.Context) error {
				current, e := validate(ctx, expected.User.Username, public)
				if e != nil {
					return e
				}
				if !expected.Same(current) {
					return fault.Proof
				}
				return a.identity.ConsumeTOTP(ctx, current.User.ID, answers[0])
			})
			if e != nil {
				return nil, fault.Proof
			}
			verified = true
			return permissions, nil
		}}}
	}
	cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		username, target, e := parseUser(meta)
		if e != nil {
			return nil, e
		}
		public, _, _, e := identity.ValidatePublicKey(string(ssh.MarshalAuthorizedKey(key)))
		if e != nil {
			return nil, fault.Proof
		}
		check, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		var p identity.NativeSSHProof
		e = a.db.Within(check, func(ctx context.Context) error {
			settings, e := a.identity.OAuthSettings(ctx)
			if e != nil || !settings.NativeEnabled || a.maintenance.Load() {
				return fault.Proof
			}
			p, e = a.identity.NativeSSHKey(ctx, username, public)
			return e
		})
		if e != nil {
			return nil, fault.Proof
		}
		return completePrimary(meta, p, public, target, "key")
	}
	configuration, err := a.system.SecurityConfiguration(ctx)
	if err != nil {
		return
	}
	if !configuration.Values.Bool(system.NativePasswordDisabledKey) && !configuration.Values.Bool("disable-password-login") {
		cfg.PasswordCallback = func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			defer clear(password)
			username, target, e := parseUser(meta)
			if e != nil || len(password) == 0 || len(password) > 1024 {
				return nil, fault.Proof
			}
			select {
			case a.hashSlots <- struct{}{}:
				defer func() { <-a.hashSlots }()
			default:
				return nil, fault.Proof
			}
			check, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			var p identity.NativeSSHProof
			accepted := false
			e = a.db.Within(check, func(ctx context.Context) error {
				settings, e := a.identity.OAuthSettings(ctx)
				if e != nil || !settings.NativeEnabled || a.maintenance.Load() {
					return fault.Proof
				}
				if e = a.identity.CheckLock(ctx, username, ip); e != nil {
					return e
				}
				u, e := a.identity.Verify(ctx, username, string(password))
				if e != nil {
					return e
				}
				if u == nil {
					return a.identity.FailedPassword(ctx, username, ip)
				} // commit failed counters
				p, e = a.identity.NativeSSHPasswordProof(ctx, username)
				if e != nil {
					return e
				}
				accepted = true
				return nil
			})
			if e != nil || !accepted {
				return nil, fault.Proof
			}
			return completePrimary(meta, p, "", target, "password")
		}
	}
	peer, channels, requests, e := ssh.NewServerConn(&nativeSSHConn{Conn: raw}, cfg)
	if e != nil {
		logCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = a.audit.Operation(logCtx, "", "native.ssh_auth", "failed", "native-ssh", ip)
		return
	}
	defer peer.Close()
	peerDone := make(chan struct{})
	go func() { defer close(peerDone); _ = peer.Wait(); cancel() }()
	defer func() { cancel(); _ = peer.Close(); <-peerDone }()
	_ = raw.SetDeadline(time.Time{})
	if peer.Permissions == nil || peer.Permissions.Extensions["native-key"] != proof.KeyID || peer.Permissions.Extensions["native-method"] != authMethod {
		return
	}
	var id string
	check, stop := context.WithTimeout(ctx, 5*time.Second)
	e = a.db.Within(check, func(ctx context.Context) error {
		current, e := validate(ctx, proof.User.Username, canonical)
		if e != nil {
			return e
		}
		if !proof.Same(current) || (current.NeedsMFA && !verified) {
			return fault.Proof
		}
		var assetID string
		if e = a.db.Row(ctx, `SELECT a.id FROM assets a JOIN resource_registry r ON r.id=a.id WHERE a.alias=$1 AND r.deleted_at IS NULL`, alias).Scan(&assetID); e != nil {
			return fault.Denied
		}
		asset, e := a.sessionAsset(ctx, &current.User, assetID)
		if e != nil {
			return e
		}
		if asset.Protocol != "ssh" {
			return fault.Denied
		}
		if e = a.enforcePolicy(ctx, &current.User, asset.ID, ip, "connect", ""); e != nil {
			return e
		}
		cred, e := a.vault.Get(ctx, asset.CredentialID)
		if e != nil {
			return e
		}
		trust, e := a.connectivity.Get(ctx, asset.ID)
		if e != nil {
			return e
		}
		if trust.Host != asset.Host || trust.Port != asset.Port {
			return fault.Denied
		}
		ticket := identity.NewToken()
		auth := identity.NewToken()
		if verified {
			if e = a.identity.RecordAccessMFA(ctx, current.User.ID, identity.Digest(auth)); e != nil {
				return e
			}
		}
		id, e = a.sessions.Reserve(ctx, session.Session{ActorID: current.User.ID, AssetID: asset.ID, AssetVersion: asset.Version, CredentialVersion: cred.Version, HostKeyVersion: trust.Version, Protocol: "ssh", ClientIP: ip}, identity.Digest(ticket), identity.Digest(auth))
		if e != nil {
			return e
		}
		settings, e := a.identity.OAuthSettings(ctx)
		if e != nil {
			return e
		}
		if _, e = a.db.Exec(ctx, `INSERT INTO native_ssh_sessions(session_id,key_id,user_version,auth_epoch,server_version,mfa_digest,mfa_verified,auth_method) VALUES($1,nullif($2,'')::uuid,$3,$4,$5,$6,$7,$8)`, id, current.KeyID, current.Version, current.Epoch, settings.Version, current.MFADigest, verified, authMethod); e != nil {
			return e
		}
		if e = a.sessions.Consume(ctx, id, current.User.ID, identity.Digest(ticket), identity.Digest(auth)); e != nil {
			return e
		}
		if e = a.audit.OpenAccess(ctx, id, current.User.ID, asset.ID, current.User.Username, asset.Name, fmt.Sprintf("%s:%d", asset.Host, asset.Port), ip); e != nil {
			return e
		}
		if e = a.audit.Login(ctx, current.User.ID, "succeeded", id, ip); e != nil {
			return e
		}
		action := "native.ssh_key_authenticated"
		if authMethod == "password" {
			if e = a.identity.Unlock(ctx, current.User.Username); e != nil {
				return e
			}
			action = "native.ssh_password_authenticated"
		}
		return a.audit.Detail(ctx, current.User.ID, action, id, ip, map[string]string{"sessionId": id, "keyId": current.KeyID, "assetId": asset.ID, "method": authMethod})
	})
	stop()
	if e != nil {
		return
	}
	v, e := a.sessions.Get(ctx, id)
	if e != nil {
		a.finishSession(id, "failed", "native_setup_failed")
		return
	}
	state, reason := "failed", "native_setup_failed"
	defer func() { a.finishSession(id, state, reason) }()
	l := &liveSession{ctx: ctx, cancel: cancel, started: time.Now(), commandSlot: make(chan struct{}, 1)}
	var output ssh.Channel
	l.sendOverride = func(typ byte, value string) error {
		if typ != '6' {
			return nil
		}
		p, e := base64.StdEncoding.DecodeString(value)
		if e != nil {
			return e
		}
		_, e = output.Write(p)
		return e
	}
	a.liveMu.Lock()
	a.live[id] = l
	a.liveMu.Unlock()
	defer func() {
		l.stop("native_closed")
		a.liveMu.Lock()
		delete(a.live, id)
		a.liveMu.Unlock()
		reason = l.reason
	}()
	auth := "ssh:" + id
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				check, stop := context.WithTimeout(ctx, time.Second)
				_, e := a.checkNativeLive(check, v, auth)
				if e == nil {
					e = a.sessions.Renew(check, id)
				}
				stop()
				if e != nil {
					l.stop(sessionLeaseReason(e, "authorization_lost"))
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-leaseDone }()
	go ssh.DiscardRequests(requests)
	setup := time.NewTimer(60 * time.Second)
	defer setup.Stop()
	var incoming ssh.NewChannel
	for incoming == nil {
		select {
		case <-ctx.Done():
			return
		case <-setup.C:
			return
		case ch, ok := <-channels:
			if !ok {
				return
			}
			if ch.ChannelType() == "direct-tcpip" {
				e = a.db.Within(ctx, func(ctx context.Context) error {
					if _, e := a.db.Exec(ctx, `UPDATE sessions SET native_forward=true WHERE id=$1`, id); e != nil {
						return e
					}
					_, e := a.db.Exec(ctx, `UPDATE access_logs SET target='受控 TCP 转发（目标见连接明细）' WHERE session_id=$1`, id)
					return e
				})
				if e != nil {
					_ = ch.Reject(ssh.ConnectionFailed, "Audit unavailable")
					return
				}
				a.nativeForwardLoop(l, v, auth, ch, channels)
				state = "closed"
				return
			}
			if ch.ChannelType() != "session" {
				_ = ch.Reject(ssh.Prohibited, "Only an audited session is permitted")
				continue
			}
			incoming = ch
		}
	}
	output, requests, e = incoming.Accept()
	if e != nil {
		return
	}
	defer output.Close()
	go func() {
		for ch := range channels {
			_ = ch.Reject(ssh.Prohibited, "Only one session is permitted")
		}
	}()
	e = a.nativeSSHChannel(l, v, auth, output, requests)
	state = "closed"
	if e != nil {
		state = "failed"
		l.stop("native_transport_failed")
	}
}
