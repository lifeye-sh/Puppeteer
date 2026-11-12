package application

import (
	"context"
	"log/slog"
	"net/http"
	"next-terminal/backend/internal/modules/approval"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/platform/fault"
	"time"
)

func (a *App) routesR08Notifications(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/notifications", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		s := a.notifications()
		channels, e := s.Channels(r.Context())
		if e != nil {
			return e
		}
		rules, e := s.Rules(r.Context())
		if e != nil {
			return e
		}
		bindings, e := s.Bindings(r.Context(), u.ID, true)
		if e != nil {
			return e
		}
		deliveries, e := s.Deliveries(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"channels": channels, "rules": rules, "bindings": bindings, "deliveries": deliveries})
		return nil
	})
	for _, kind := range []string{"channels", "rules", "bindings"} {
		save := func(w http.ResponseWriter, r *http.Request) error {
			_, e := a.require(r, "identity.manage")
			if e != nil {
				return e
			}
			var channel notification.Channel
			var rule notification.Rule
			var binding notification.Binding
			switch kind {
			case "channels":
				e = decode(w, r, &channel)
				channel.ID = r.PathValue("id")
			case "rules":
				e = decode(w, r, &rule)
				rule.ID = r.PathValue("id")
			case "bindings":
				e = decode(w, r, &binding)
			}
			if e != nil {
				return e
			}
			var id string
			e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
				if e := a.allowed(ctx, u, "identity.manage"); e != nil {
					return e
				}
				var e error
				switch kind {
				case "channels":
					id, e = a.notifications().SaveChannel(ctx, channel)
				case "rules":
					id, e = a.notifications().SaveRule(ctx, rule)
				case "bindings":
					e = a.notifications().Bind(ctx, binding)
					id = binding.UserID
				}
				if e != nil {
					return e
				}
				return a.audit.Detail(ctx, u.ID, "notification."+kind+".saved", requestID(r), sourceIP(r), map[string]string{"id": id})
			})
			if e != nil {
				return e
			}
			writeJSON(w, 200, map[string]string{"id": id})
			return nil
		}
		h("POST /api/admin/notifications/"+kind, save)
		if kind != "bindings" {
			h("PUT /api/admin/notifications/"+kind+"/{id}", save)
		}
	}
	h("POST /api/admin/notifications/channels/{id}/test", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var id string
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			var e error
			id, e = a.notifications().Test(ctx, r.PathValue("id"), u.ID)
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "notification.test", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 202, map[string]string{"id": id})
		return nil
	})
	h("POST /api/admin/notifications/deliveries/{id}/retry", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.notifications().Retry(ctx, r.PathValue("id")); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "notification.retry", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("GET /api/account/notifications", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		p, e := a.notifications().Preference(r.Context(), u.ID)
		if e != nil {
			return e
		}
		bindings, e := a.notifications().Bindings(r.Context(), u.ID, false)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"preference": p, "bindings": bindings})
		return nil
	})
	h("PUT /api/account/notifications/preference", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		var p notification.Preference
		if e = decode(w, r, &p); e != nil {
			return e
		}
		if e = a.notifications().SetPreference(r.Context(), u.ID, p); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	// Addresses are provisioned by administrators to avoid sending approval details to unverified destinations.
	h("PUT /api/account/notifications/bindings/{id}", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		bindings, e := a.notifications().Bindings(r.Context(), u.ID, false)
		if e != nil {
			return e
		}
		for _, b := range bindings {
			if b.ChannelID == r.PathValue("id") {
				b.Enabled = in.Enabled
				if e = a.notifications().Bind(r.Context(), b); e != nil {
					return e
				}
				w.WriteHeader(204)
				return nil
			}
		}
		return fault.Denied
	})
}

func (a *App) notificationEligible(ctx context.Context, delivery, user string) (bool, error) {
	var request, actor, mode, databaseOrder, systemEvent string
	var enabled bool
	e := a.db.Row(ctx, `SELECT coalesce(e.request_id::text,''),e.actor_id::text,r.recipient_mode,r.enabled,coalesce(e.database_order_id::text,''),coalesce(e.system_event_id::text,'') FROM notification_deliveries d JOIN notification_events e ON e.id=d.event_id JOIN notification_rules r ON r.id=d.rule_id WHERE d.id=$1`, delivery).Scan(&request, &actor, &mode, &enabled, &databaseOrder, &systemEvent)
	if e != nil {
		return false, e
	}
	if !enabled {
		return false, nil
	}
	if systemEvent != "" {
		subject, e := a.identity.Subject(ctx, user)
		if e != nil {
			return false, e
		}
		return mode == "specified" && subject.Enabled && a.allowed(ctx, &subject.User, "identity.manage") == nil, nil
	}
	if databaseOrder != "" {
		return a.databaseNotificationEligible(ctx, databaseOrder, user, actor, mode)
	}
	if mode == "requester" {
		return actor == user, nil
	}
	subject, e := a.identity.Subject(ctx, user)
	if e != nil {
		return false, e
	}
	if !subject.Enabled {
		return false, nil
	}
	if mode == "specified" {
		return a.allowed(ctx, &subject.User, "approval.read") == nil, nil
	}
	v, e := a.approvals().Get(ctx, request, false)
	if e != nil {
		return false, e
	}
	scope, e := a.approvals().Scope(ctx, v.ScopeID, false)
	if e != nil {
		return false, e
	}
	return v.Status == "pending" && v.ActorID != user && approval.CanReview(scope, user) && a.allowed(ctx, &subject.User, "approval.review") == nil, nil
}

func (a *App) ExpireApprovals(ctx context.Context) error {
	ids, e := a.approvals().Expiring(ctx)
	if e != nil {
		return e
	}
	for _, id := range ids {
		e = a.db.Within(ctx, func(ctx context.Context) error {
			v, e := a.approvals().Get(ctx, id, true)
			if e != nil {
				return e
			}
			if v.Status != "approved" || v.ExpiresAt == nil || v.ExpiresAt.After(time.Now()) {
				return nil
			}
			if _, e = a.db.Exec(ctx, `UPDATE grants SET revoked_at=coalesce(revoked_at,now()),version=version+1 WHERE id=$1 AND approval_id=$2`, v.GrantID, v.ID); e != nil {
				return e
			}
			if e = a.approvals().Decide(ctx, v, "expired", v.ReviewerID, "授权已到期", v.GrantID, v.StartsAt, v.ExpiresAt); e != nil {
				return e
			}
			if e = a.notifications().Enqueue(ctx, v.ID+":expired", "access.expired", v.ID, v.ActorID, nil); e != nil {
				return e
			}
			return a.audit.Detail(ctx, v.ActorID, "approval.expired", "worker", "", map[string]string{"id": v.ID, "grantId": v.GrantID})
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func (a *App) RunApprovalMaintenance(ctx context.Context) {
	nextIdentityCleanup := time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(nextIdentityCleanup) {
				if e := a.identity.CleanupFederation(ctx); e != nil && ctx.Err() == nil {
					slog.Error("identity expiry cleanup failed")
				}
				nextIdentityCleanup = time.Now().Add(time.Minute)
			}
			if e := a.ExpireApprovals(ctx); e != nil && ctx.Err() == nil {
				slog.Error("approval expiry failed")
			}
			if e := a.deliverContactVerification(ctx); e != nil && ctx.Err() == nil {
				slog.Error("identity verification delivery failed")
			}
			if e := a.notifications().Expand(ctx); e != nil && ctx.Err() == nil {
				slog.Error("notification expansion failed")
			}
			if e := a.notifications().DeliverOne(ctx, a.notificationEligible); e != nil && ctx.Err() == nil {
				slog.Error("notification delivery state update failed")
			}
		}
	}
}
