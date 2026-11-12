package application

import (
	"context"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

func (a *App) routesJump(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /api/admin/ssh-jumps", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.connectivity.Jumps(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	save := func(w http.ResponseWriter, r *http.Request) error {
		var in connectivity.Jump
		if e := decode(w, r, &in); e != nil {
			return e
		}
		in.ID = r.PathValue("id")
		var id string
		e := a.mutate(r, "ssh.jump_saved", func(ctx context.Context, u *identity.User) (string, error) {
			if _, e := a.vault.Get(ctx, in.CredentialID); e != nil {
				return "", e
			}
			var e error
			id, e = a.connectivity.SaveJump(ctx, in)
			return id, e
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	handle("POST /api/admin/ssh-jumps", save)
	handle("PUT /api/admin/ssh-jumps/{id}", save)
	handle("POST /api/admin/ssh-jumps/{id}/discover", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		if !a.allowAttempt("ssh-discovery:" + sourceIP(r)) {
			return problem(429, "rate_limited", "请稍后再探测")
		}
		select {
		case a.probeSlots <- struct{}{}:
			defer func() { <-a.probeSlots }()
		default:
			return problem(429, "busy", "探测并发已满")
		}
		v, e := a.connectivity.Jump(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var transport connectivity.Dialer
		if v.ParentID != "" {
			transport = a.jumpDialer(ctx, v.ParentID, u.ID, "jump-chain-probe")
		}
		observed, e := connectivity.DiscoverUsing(ctx, v.Host, v.Port, transport)
		if e != nil {
			return problem(422, "ssh_discovery_failed", "无法读取跳板主机密钥，请检查网络和端口")
		}
		if e = a.audit.Detail(r.Context(), u.ID, "ssh.jump_key_observed", requestID(r), sourceIP(r), map[string]string{"target": v.ID, "fingerprint": observed.Fingerprint}); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"observed": observed, "version": v.Version})
		return nil
	})
	handle("POST /api/admin/ssh-jumps/{id}/trust", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Version   int64  `json:"version"`
			PublicKey string `json:"publicKey"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		e := a.mutate(r, "ssh.jump_key_trusted", func(ctx context.Context, u *identity.User) (string, error) {
			v, e := a.connectivity.Jump(ctx, r.PathValue("id"))
			if e != nil {
				return "", e
			}
			if e = a.connectivity.TrustJump(ctx, v.ID, in.Version, in.PublicKey); e != nil {
				return "", e
			}
			v, e = a.connectivity.Jump(ctx, v.ID)
			if e != nil {
				return "", e
			}
			return v.ID, a.audit.Detail(ctx, u.ID, "ssh.jump_key_fingerprint", requestID(r), sourceIP(r), map[string]string{"target": v.ID, "fingerprint": v.Fingerprint})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}

type cancelJumpConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *cancelJumpConn) Close() error { c.cancel(); return c.Conn.Close() }

// Only called after asset authorization. Jump metadata and credentials never come from the browser dial request.
func (a *App) jumpDialer(life context.Context, jumpID, actor, sessionID string) connectivity.Dialer {
	return a.jumpDialerDepth(life, jumpID, actor, sessionID, 0)
}
func (a *App) jumpDialerDepth(life context.Context, jumpID, actor, sessionID string, depth int) connectivity.Dialer {
	return func(ctx context.Context, host string, port int) (net.Conn, error) {
		if depth >= 4 {
			return nil, fault.Invalid
		}
		var j connectivity.Jump
		var cred vault.Credential
		var auth ssh.AuthMethod
		e := a.db.Within(ctx, func(c context.Context) error {
			var e error
			j, e = a.connectivity.Jump(c, jumpID)
			if e != nil {
				return e
			}
			if !j.Enabled || j.PublicKey == "" {
				return fault.Denied
			}
			cred, auth, e = a.vault.SSHAuth(c, j.CredentialID, actor, sessionID)
			return e
		})
		if e != nil {
			return nil, e
		}
		owned, cancel := context.WithCancel(life)
		var transport connectivity.Dialer
		if j.ParentID != "" {
			transport = a.jumpDialerDepth(owned, j.ParentID, actor, sessionID, depth+1)
		}
		conn, e := connectivity.DialJumpUsing(owned, ctx, j, cred.Username, auth, host, port, transport)
		auth = nil
		if e != nil {
			cancel()
			return nil, e
		}
		go func() {
			defer conn.Close()
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-owned.Done():
					return
				case <-tick.C:
					c, stop := context.WithTimeout(owned, time.Second)
					latest, err := a.connectivity.Jump(c, jumpID)
					if err == nil && (!latest.Enabled || latest.Version != j.Version) {
						err = fault.Denied
					}
					if err == nil {
						v, e := a.vault.Get(c, j.CredentialID)
						err = e
						if err == nil && v.Version != cred.Version {
							err = fault.Denied
						}
					}
					stop()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()
		return &cancelJumpConn{Conn: conn, cancel: cancel}, nil
	}
}
