package application

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
)

func (a *App) nativeAssets(w http.ResponseWriter, r *http.Request) error {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || len(header) != 71 || token(r) != "" {
		return problem(401, "invalid_token", "需要原生客户端访问令牌")
	}
	limit, offset := 100, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 1 || v > 100 {
			return fault.Invalid
		}
		limit = v
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || len(b) > 8 {
			return fault.Invalid
		}
		offset, e = strconv.Atoi(string(b))
		if e != nil || offset < 0 || offset > 10000000 {
			return fault.Invalid
		}
	}
	items := []map[string]any{}
	next := ""
	e := a.db.Within(r.Context(), func(ctx context.Context) error {
		u, e := a.identity.NativeUser(ctx, strings.TrimPrefix(header, "Bearer "), "assets:read")
		if e != nil {
			return e
		}
		f := resource.Filter{All: u.Type == "admin", EnabledOnly: true, Offset: offset, Limit: limit}
		if !f.All {
			deps, e := a.identity.DepartmentIDs(ctx, u.ID)
			if e != nil {
				return e
			}
			f.Targets, e = a.authz.Targets(ctx, u.ID, deps)
			if e != nil {
				return e
			}
		}
		assets, total, e := a.resources.List(ctx, f)
		if e != nil {
			return e
		}
		forward, e := a.forwardSettings(ctx)
		if e != nil {
			return e
		}
		for _, v := range assets {
			entries := []string{}
			if v.Protocol == "rdp" && a.nativeRDPAddress.Load() != nil && v.CertFingerprint != "" {
				entries = append(entries, "rdp-view")
			}
			if v.Protocol == "ssh" {
				entries = append(entries, "ssh")
				if forward.Enabled {
					for _, rule := range forward.Rules {
						if rule.AssetID == v.ID {
							entries = append(entries, "ssh-forward")
							break
						}
					}
				}
			}
			items = append(items, map[string]any{"id": v.ID, "name": v.Name, "alias": v.Alias, "protocol": v.Protocol, "revision": v.Version, "entries": entries})
		}
		if offset+len(assets) < total {
			next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(assets))))
		}
		return nil
	})
	if e != nil {
		if errors.Is(e, fault.Proof) {
			return problem(401, "invalid_token", "原生客户端令牌已失效")
		}
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items, "nextCursor": next})
	return nil
}
