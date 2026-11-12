package application

import (
	"context"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

func (a *App) routesGroups(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /api/admin/gateway-groups", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.connectivity.Groups(r.Context())
		if e == nil {
			writeJSON(w, 200, v)
		}
		return e
	})
	save := func(w http.ResponseWriter, r *http.Request) error {
		var v connectivity.GatewayGroup
		if e := decode(w, r, &v); e != nil {
			return e
		}
		v.ID = r.PathValue("id")
		var id string
		e := a.mutate(r, "gateway.group_saved", func(ctx context.Context, u *identity.User) (string, error) {
			var e error
			id, e = a.connectivity.SaveGroup(ctx, v)
			return id, e
		})
		if e == nil {
			writeJSON(w, 200, map[string]string{"id": id})
		}
		return e
	}
	handle("POST /api/admin/gateway-groups", save)
	handle("PUT /api/admin/gateway-groups/{id}", save)
}

// Ordered failover only while opening a new transport; a live SSH session is never replayed.
func (a *App) groupDialer(life context.Context, id, actor, lease string, authorize func(context.Context) error) connectivity.Dialer {
	return func(ctx context.Context, host string, port int) (net.Conn, error) {
		g, e := a.connectivity.Group(ctx, id)
		if e != nil {
			return nil, e
		}
		if !g.Enabled {
			return nil, fault.Denied
		}
		owned, cancel := context.WithCancel(life)
		var conn net.Conn
		for i, m := range g.Members {
			if e = authorize(ctx); e != nil {
				break
			}
			remaining := len(g.Members) - i
			d := 3 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				if slice := time.Until(deadline) / time.Duration(remaining); slice < d {
					d = slice
				}
			}
			attempt, done := context.WithTimeout(ctx, d)
			if m.Kind == "agent" {
				conn, e = a.connectivity.DialAgent(owned, attempt, m.ID, lease, host, port, authorize)
			} else {
				conn, e = a.jumpDialer(owned, m.ID, actor, lease)(attempt, host, port)
			}
			done()
			if e == nil {
				break
			}
		}
		if e != nil || conn == nil {
			cancel()
			return nil, connectivity.ErrAgentOffline
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
					c, done := context.WithTimeout(owned, time.Second)
					latest, e := a.connectivity.Group(c, id)
					if e == nil {
						e = authorize(c)
					}
					done()
					if e != nil || !latest.Enabled || latest.Version != g.Version {
						cancel()
						return
					}
				}
			}
		}()
		return &cancelJumpConn{Conn: conn, cancel: cancel}, nil
	}
}
