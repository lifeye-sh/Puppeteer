package application

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/secretbox"
	"regexp"
	"strings"
	"time"
)

var nativeIdempotency = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func nativeBearer(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if r.Header.Get("Origin") != "" || token(r) != "" || r.URL.RawQuery != "" || !strings.HasPrefix(h, "Bearer ") || len(h) != 71 {
		return "", problem(401, "invalid_token", "需要原生客户端令牌")
	}
	return h[7:], nil
}
func (a *App) nativeTicketHash(ticket string) string {
	mac := hmac.New(sha256.New, a.config.EncryptionKey)
	_, _ = mac.Write([]byte("native_bridge:" + ticket))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *App) nativeConnect(w http.ResponseWriter, r *http.Request) error {
	bearer, e := nativeBearer(r)
	if e != nil {
		return e
	}
	var in struct {
		AssetID         string `json:"assetId"`
		Entry           string `json:"entry"`
		IdempotencyKey  string `json:"idempotencyKey"`
		ClientVersion   string `json:"clientVersion"`
		ClientPublicKey string `json:"clientPublicKey"`
		Readonly        bool   `json:"readonly"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if !resource.ValidID(in.AssetID) || (in.Entry != "ssh" && in.Entry != "ssh-forward") || !nativeIdempotency.MatchString(in.IdempotencyKey) || len(in.ClientVersion) > 100 {
		return fault.Invalid
	}
	ticket := identity.NewToken()
	public, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(in.ClientPublicKey))
	if e != nil || public.Type() != ssh.KeyAlgoED25519 || len(options) != 0 || len(rest) != 0 || in.ClientPublicKey != strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))) {
		return fault.Invalid
	}
	var out map[string]any
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		u, e := a.identity.NativeUser(ctx, bearer, "sessions:connect")
		if e != nil {
			return e
		}
		g, _, e := a.identity.OAuthToken(ctx, bearer, "access", identity.NativeClientUUID, false)
		if e != nil {
			return e
		}
		asset, e := a.sessionAsset(ctx, u, in.AssetID)
		if e != nil {
			return e
		}
		if asset.Protocol != "ssh" {
			return problem(409, "entry_unavailable", "此资产不支持原生 SSH")
		}
		if e = a.enforcePolicy(ctx, u, asset.ID, sourceIP(r), "connect", ""); e != nil {
			return e
		}
		if in.Entry == "ssh-forward" {
			settings, err := a.forwardSettings(ctx)
			if err != nil {
				return err
			}
			if !settings.Enabled || in.Readonly {
				return fault.Denied
			}
			matched := false
			for _, rule := range settings.Rules {
				if rule.AssetID == asset.ID {
					matched = true
				}
			}
			if !matched {
				return fault.Denied
			}
			for _, action := range []string{"execute", "upload", "download"} {
				d, err := a.policyDecision(ctx, u, asset.ID, sourceIP(r), action, "")
				if err != nil {
					return err
				}
				if !d.Allowed || d.Filtered {
					return fault.Denied
				}
			}
		}
		// LockUser in NativeUser serializes idempotency checks for this actor.
		var existing, existingAsset, state, existingKey string
		var ro, forward bool
		e = a.db.Row(ctx, `SELECT s.id,s.asset_id,s.state,s.readonly,n.client_key,s.native_forward FROM native_bridge_reservations n JOIN sessions s ON s.id=n.session_id WHERE n.actor_id=$1 AND n.idempotency_key=$2`, u.ID, in.IdempotencyKey).Scan(&existing, &existingAsset, &state, &ro, &existingKey, &forward)
		if e == nil {
			if existingAsset != asset.ID || ro != in.Readonly || existingKey != in.ClientPublicKey || forward != (in.Entry == "ssh-forward") {
				return fault.Conflict
			}
			out = map[string]any{"sessionId": existing, "state": state, "replayed": true}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if !a.allowAttempt("ssh-create:" + u.ID) {
			return problem(429, "rate_limited", "会话创建过于频繁")
		}
		cred, e := a.vault.Get(ctx, asset.CredentialID)
		if e != nil {
			return e
		}
		trust, e := a.connectivity.Get(ctx, asset.ID)
		if e != nil {
			return problem(409, "host_key_untrusted", "请先在资产中核对并信任 SSH 主机密钥")
		}
		if trust.Host != asset.Host || trust.Port != asset.Port {
			return fault.Conflict
		}
		id, e := a.sessions.Reserve(ctx, session.Session{ActorID: u.ID, AssetID: asset.ID, Protocol: "ssh", AssetVersion: asset.Version, CredentialVersion: cred.Version, HostKeyVersion: trust.Version, ClientIP: sourceIP(r), Readonly: in.Readonly}, a.nativeTicketHash(ticket), identity.Digest(g.ID))
		if e != nil {
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
		sealed, e := secretbox.Seal(a.config.EncryptionKey, key, "native-ssh:"+id)
		if e != nil {
			return e
		}
		var expires time.Time
		e = a.db.Row(ctx, `INSERT INTO native_bridge_reservations(session_id,actor_id,family_id,idempotency_key,ticket_hash,host_key,client_key,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '60 seconds') RETURNING expires_at`, id, u.ID, g.ID, in.IdempotencyKey, a.nativeTicketHash(ticket), sealed, in.ClientPublicKey).Scan(&expires)
		if e != nil {
			return e
		}
		if _, e = a.db.Exec(ctx, `UPDATE sessions SET ticket_expires_at=$2,lease_expires_at=$2,native_forward=$3 WHERE id=$1`, id, expires, in.Entry == "ssh-forward"); e != nil {
			return e
		}
		if e = a.audit.OpenAccess(ctx, id, u.ID, asset.ID, u.Username, asset.Name, fmt.Sprintf("%s:%d", asset.Host, asset.Port), sourceIP(r)); e != nil {
			return e
		}
		if in.Entry == "ssh-forward" {
			if _, e = a.db.Exec(ctx, `UPDATE access_logs SET target='受控 TCP 转发（目标见连接明细）' WHERE session_id=$1`, id); e != nil {
				return e
			}
		}
		if e = a.audit.Detail(ctx, u.ID, "native.ssh_reserved", requestID(r), sourceIP(r), map[string]string{"sessionId": id, "assetId": asset.ID}); e != nil {
			return e
		}
		out = map[string]any{"sessionId": id, "bridgeTicket": ticket, "expiresAt": expires, "bridgeUrl": "/api/native/v1/bridge", "hostKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), "capabilities": map[string]bool{"shell": in.Entry == "ssh", "exec": in.Entry == "ssh" && !in.Readonly, "sftp": false, "forwarding": in.Entry == "ssh-forward"}}
		return nil
	})
	if errors.Is(e, fault.Proof) {
		return problem(401, "invalid_token", "原生客户端令牌已失效")
	}
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
	return nil
}

func (a *App) checkNativeLive(ctx context.Context, v session.Session, family string) (resource.Asset, error) {
	var asset resource.Asset
	e := a.db.Within(ctx, func(ctx context.Context) error {
		u, e := a.nativeSessionUser(ctx, v, family)
		if e != nil {
			return e
		}
		asset, e = a.checkLiveUser(ctx, v, u)
		return e
	})
	return asset, e
}

func (a *App) nativeTerminate(w http.ResponseWriter, r *http.Request) error {
	bearer, e := nativeBearer(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		u, e := a.identity.NativeUser(ctx, bearer, "sessions:connect")
		if e != nil {
			return e
		}
		var owner string
		if e = a.db.Row(ctx, `SELECT actor_id FROM native_bridge_reservations WHERE session_id=$1`, id).Scan(&owner); e != nil {
			return fault.Denied
		}
		if owner != u.ID {
			return fault.Denied
		}
		tag, e := a.db.Exec(ctx, `UPDATE sessions SET terminate_requested=true WHERE id=$1 AND closed_at IS NULL AND NOT terminate_requested`, id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return a.audit.Detail(ctx, u.ID, "native.session_terminated", requestID(r), sourceIP(r), map[string]string{"sessionId": id})
	})
	if e != nil {
		return e
	}
	a.liveMu.Lock()
	l := a.live[id]
	a.liveMu.Unlock()
	if l != nil {
		l.stop("owner_terminated")
	}
	w.WriteHeader(204)
	return nil
}
