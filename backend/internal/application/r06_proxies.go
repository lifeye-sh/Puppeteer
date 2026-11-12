package application

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/netproxy"
	"strconv"
	"time"
)

func (a *App) routesProxies(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /api/admin/network-proxies", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.connectivity.Proxies(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	save := func(w http.ResponseWriter, r *http.Request) error {
		var v connectivity.Proxy
		if e := decode(w, r, &v); e != nil {
			return e
		}
		v.ID = r.PathValue("id")
		var id string
		e := a.mutate(r, "network.proxy_saved", func(ctx context.Context, u *identity.User) (string, error) {
			if v.CredentialID != "" {
				c, e := a.vault.Get(ctx, v.CredentialID)
				if e != nil {
					return "", e
				}
				if c.Kind != "password" {
					return "", fault.Invalid
				}
			}
			var e error
			id, e = a.connectivity.SaveProxy(ctx, v)
			return id, e
		})
		if e == nil {
			writeJSON(w, 200, map[string]string{"id": id})
		}
		return e
	}
	handle("POST /api/admin/network-proxies", save)
	handle("PUT /api/admin/network-proxies/{id}", save)
}
func (a *App) proxyDialer(life context.Context, id, actor, lease string) connectivity.Dialer {
	return func(ctx context.Context, host string, port int) (net.Conn, error) {
		v, e := a.connectivity.Proxy(ctx, id)
		if e != nil {
			return nil, e
		}
		if !v.Enabled {
			return nil, fault.Denied
		}
		u := &url.URL{Scheme: v.Scheme, Host: net.JoinHostPort(v.Host, strconv.Itoa(v.Port))}
		var version int64
		if v.CredentialID != "" {
			e = a.db.Within(ctx, func(c context.Context) error {
				cred, secret, e := a.vault.ProxyAuth(c, v.CredentialID, actor, lease)
				if e != nil {
					return e
				}
				version = cred.Version
				u.User = url.UserPassword(cred.Username, secret)
				return nil
			})
			if e != nil {
				return nil, e
			}
		}
		conn, e := netproxy.Dial(ctx, u, net.JoinHostPort(host, strconv.Itoa(port)))
		u.User = nil
		if e != nil {
			return nil, e
		}
		owned, cancel := context.WithCancel(life)
		go func() {
			defer conn.Close()
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-owned.Done():
					return
				case <-tick.C:
					c, done := context.WithTimeout(owned, time.Second)
					current, e := a.connectivity.Proxy(c, id)
					if e == nil && v.CredentialID != "" {
						cred, err := a.vault.Get(c, v.CredentialID)
						e = err
						if e == nil && cred.Version != version {
							e = fault.Denied
						}
					}
					done()
					if e != nil || !current.Enabled || current.Version != v.Version {
						cancel()
						return
					}
				}
			}
		}()
		return &cancelJumpConn{Conn: conn, cancel: cancel}, nil
	}
}
