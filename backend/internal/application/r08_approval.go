package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/approval"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) approvals() *approval.Service         { return approval.New(a.db) }
func (a *App) notifications() *notification.Service { return notification.New(a.db, a.config.VaultKey) }

func (a *App) approvalEligible(ctx context.Context, scope approval.Scope, user, asset string) error {
	subject, e := a.identity.Subject(ctx, user)
	if e != nil {
		return e
	}
	if !subject.Enabled {
		return fault.Denied
	}
	ids, e := a.resources.Scope(ctx, asset)
	if e != nil {
		return e
	}
	if !approval.Matches(scope, user, subject.DepartmentIDs, ids) {
		return fault.Denied
	}
	assets, _, e := a.resources.List(ctx, resource.Filter{ID: asset, All: true, EnabledOnly: true, Limit: 1})
	if e != nil {
		return e
	}
	if len(assets) != 1 {
		return fault.Denied
	}
	return nil
}

func (a *App) routesR08Approval(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/approval-scopes", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "approval.read"); e != nil {
			return e
		}
		v, e := a.approvals().Scopes(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	save := func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in approval.Scope
		if e = decode(w, r, &in); e != nil {
			return e
		}
		in.ID = r.PathValue("id")
		var id string
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			for _, user := range in.ReviewerIDs {
				subject, e := a.identity.Subject(ctx, user)
				if e != nil {
					return e
				}
				if !subject.Enabled || a.allowed(ctx, &subject.User, "approval.review") != nil {
					return problem(400, "reviewer_permission", "审批人必须启用且具有审批权限")
				}
			}
			var e error
			id, e = a.approvals().SaveScope(ctx, in)
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "approval.scope.saved", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	h("POST /api/admin/approval-scopes", save)
	h("PUT /api/admin/approval-scopes/{id}", save)
	h("GET /api/portal/requestable-assets", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		scopes, e := a.approvals().Scopes(r.Context())
		if e != nil {
			return e
		}
		deps, e := a.identity.DepartmentIDs(r.Context(), u.ID)
		if e != nil {
			return e
		}
		assets, _, e := a.resources.List(r.Context(), resource.Filter{All: true, EnabledOnly: true, Limit: 1000})
		if e != nil {
			return e
		}
		out := []map[string]any{}
		for _, asset := range assets {
			ids, e := a.resources.Scope(r.Context(), asset.ID)
			if e != nil {
				return e
			}
			for _, scope := range scopes {
				if approval.Matches(scope, u.ID, deps, ids) {
					out = append(out, map[string]any{"id": asset.ID, "name": asset.Name, "protocol": asset.Protocol, "scopeId": scope.ID, "scopeName": scope.Name, "maxMinutes": scope.MaxMinutes})
				}
			}
		}
		writeJSON(w, 200, out)
		return nil
	})
	h("GET /api/access-requests", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 || page > 10000 {
			page = 1
		}
		v, total, e := a.approvals().List(r.Context(), u.ID, u.Type == "admin", page)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": v, "total": total})
		return nil
	})
	h("POST /api/access-requests", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		var in approval.Request
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if !resource.ValidID(in.ScopeID) || !resource.ValidID(in.ResourceID) {
			return fault.Invalid
		}
		in.ActorID = u.ID
		var id string
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			scope, e := a.approvals().Scope(ctx, in.ScopeID, true)
			if e != nil {
				return e
			}
			if e = a.approvalEligible(ctx, scope, u.ID, in.ResourceID); e != nil {
				return e
			}
			if in.Duration > scope.MaxMinutes {
				return fault.Invalid
			}
			id, e = a.approvals().Submit(ctx, in)
			if e != nil {
				return e
			}
			if e = a.notifications().Enqueue(ctx, id+":submitted", "access.submitted", id, u.ID, scope.ReviewerIDs); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "approval.submitted", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 201, map[string]string{"id": id})
		return nil
	})
	h("POST /api/access-requests/{id}/decision", a.decideAccessRequest)
}

func (a *App) decideAccessRequest(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	var in struct {
		Version         int64  `json:"version"`
		Action          string `json:"action"`
		Reason          string `json:"reason"`
		Duration        int    `json:"durationMinutes"`
		StrategyID      string `json:"strategyId"`
		CommandFilterID string `json:"commandFilterId"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		v, e := a.approvals().Get(ctx, r.PathValue("id"), true)
		if e != nil {
			return e
		}
		if v.Version != in.Version {
			return fault.Conflict
		}
		scope, e := a.approvals().Scope(ctx, v.ScopeID, true)
		if e != nil {
			return e
		}
		status := ""
		grant := v.GrantID
		start, end := v.StartsAt, v.ExpiresAt
		switch in.Action {
		case "approve", "reject":
			if v.Status != "pending" {
				return fault.Conflict
			}
			if v.ActorID == u.ID {
				return problem(403, "self_review", "不能审批自己的申请")
			}
			if !approval.CanReview(scope, u.ID) {
				return fault.Denied
			}
			if e = a.allowed(ctx, u, "approval.review"); e != nil {
				return e
			}
			status = "rejected"
			if in.Action == "approve" {
				if e = a.approvalEligible(ctx, scope, v.ActorID, v.ResourceID); e != nil {
					return e
				}
				duration := in.Duration
				if duration == 0 {
					duration = v.Duration
				}
				if duration < 1 || duration > v.Duration || duration > scope.MaxMinutes {
					return fault.Invalid
				}
				from := v.RequestedStart
				if from.Before(time.Now()) {
					from = time.Now()
				}
				until := from.Add(time.Duration(duration) * time.Minute)
				start, end = &from, &until
				if e = a.authz.RetireExpiredGrant(ctx, v.ActorID, v.ResourceID); e != nil {
					return e
				}
				grant, e = a.authz.SaveGrant(ctx, authorization.Grant{UserID: v.ActorID, ResourceID: v.ResourceID, ValidFrom: from, ExpiresAt: end, ApprovalID: v.ID, StrategyID: in.StrategyID, CommandFilterID: in.CommandFilterID})
				if e != nil {
					return e
				}
				status = "approved"
			}
		case "cancel":
			if v.ActorID != u.ID && u.Type != "admin" {
				return fault.Denied
			}
			if v.Status != "pending" && v.Status != "approved" {
				return fault.Conflict
			}
			status = "cancelled"
			if grant != "" {
				if _, e = a.db.Exec(ctx, `UPDATE grants SET revoked_at=coalesce(revoked_at,now()),version=version+1 WHERE id=$1 AND approval_id=$2`, grant, v.ID); e != nil {
					return e
				}
			}
		default:
			return fault.Invalid
		}
		if e = a.approvals().Decide(ctx, v, status, u.ID, in.Reason, grant, start, end); e != nil {
			return e
		}
		if e = a.notifications().Enqueue(ctx, v.ID+":"+status, "access."+status, v.ID, v.ActorID, scope.ReviewerIDs); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "approval."+status, requestID(r), sourceIP(r), map[string]string{"id": v.ID, "grantId": grant})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
