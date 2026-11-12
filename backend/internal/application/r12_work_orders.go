package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) databaseWorkAccess(ctx context.Context, user, asset string) error {
	subject, e := a.identity.Subject(ctx, user)
	if e != nil {
		return e
	}
	if !subject.Enabled {
		return fault.Denied
	}
	if subject.User.Type == "admin" {
		return nil
	}
	allowed, e := a.authz.DatabaseAccess(ctx, user, asset, subject.DepartmentIDs)
	if e != nil {
		return e
	}
	if !allowed {
		return fault.Denied
	}
	return nil
}
func (a *App) databaseReviewers(ctx context.Context) ([]string, error) {
	rows, e := a.db.Rows(ctx, `SELECT u.id::text FROM users u WHERE u.status='enabled' AND u.deleted_at IS NULL AND (u.type='admin' OR EXISTS(SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE ur.user_id=u.id AND rp.action='database.approve'))`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (a *App) databaseNotificationEligible(ctx context.Context, order, user, actor, mode string) (bool, error) {
	subject, e := a.identity.Subject(ctx, user)
	if e != nil {
		return false, e
	}
	if !subject.Enabled {
		return false, nil
	}
	if mode == "requester" {
		return user == actor, nil
	}
	if a.allowed(ctx, &subject.User, "database.approve") != nil {
		return false, nil
	}
	v, e := database.New(a.db).Work(ctx, order, false)
	if e != nil {
		return false, e
	}
	if a.databaseWorkAccess(ctx, user, v.AssetID) != nil {
		return false, nil
	}
	return mode == "specified" || (v.State == "pending" && user != v.RequesterID), nil
}
func (a *App) registerDatabaseWorkOrders(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, admin := range []bool{false, true} {
		prefix := "/api/portal/database-work-orders"
		if admin {
			prefix = "/api/admin/database-work-orders"
		}
		h("GET "+prefix, func(w http.ResponseWriter, r *http.Request) error {
			u, e := a.authenticated(r)
			if e != nil {
				return e
			}
			if admin {
				if e = a.allowed(r.Context(), u, "database.approve"); e != nil {
					return e
				}
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("current"))
			if page < 1 {
				page = 1
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
			if limit < 1 || limit > 100 {
				limit = 20
			}
			if page > 100000 {
				return fault.Invalid
			}
			list, total, e := database.New(a.db).Works(r.Context(), u.ID, r.URL.Query().Get("state"), admin, (page-1)*limit, limit)
			if e != nil {
				return e
			}
			writeJSON(w, 200, map[string]any{"items": list, "total": total})
			return nil
		})
	}
	h("POST /api/database-work-orders", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			AssetID        string `json:"assetId"`
			Title          string `json:"title"`
			SQL            string `json:"sql"`
			AcknowledgeDDL bool   `json:"acknowledgeDdl"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if !resource.ValidID(in.AssetID) {
			return fault.Invalid
		}
		var id string
		e := a.securityTx(r, func(c context.Context, u *identity.User) error {
			service := database.New(a.db)
			asset, e := service.Asset(c, in.AssetID)
			if e != nil {
				return e
			}
			if e = a.databaseWorkAccess(c, u.ID, asset.ID); e != nil {
				return e
			}
			if asset.Engine != "mysql" {
				return problem(409, "database_engine_unavailable", "此数据库类型尚未开放 SQL 工单执行")
			}
			if asset.WriteCredentialID == "" {
				return problem(422, "write_credential_required", "请先配置独立的审批执行凭据")
			}
			policy, e := database.NewSQLPolicy()
			if e != nil {
				return e
			}
			plan, e := policy.WorkOrder(in.SQL, asset.Database)
			if e != nil {
				return fault.Invalid
			}
			if plan.Mode == "ddl" && !in.AcknowledgeDDL {
				return problem(422, "ddl_confirmation_required", "DDL 不能整体回滚，请确认逐条执行风险")
			}
			credential, e := a.vault.Get(c, asset.WriteCredentialID)
			if e != nil {
				return e
			}
			id, e = service.CreateWork(c, database.WorkOrder{AssetID: asset.ID, RequesterID: u.ID, Title: in.Title, SQL: in.SQL, Mode: plan.Mode, AssetVersion: asset.Version, CredentialVersion: credential.ActiveVersion})
			if e != nil {
				return e
			}
			reviewers, e := a.databaseReviewers(c)
			if e != nil {
				return e
			}
			if e = a.notifications().EnqueueDatabase(c, id+":submitted", "database.submitted", id, u.ID, reviewers); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "database.work_submitted", requestID(r), sourceIP(r), map[string]string{"id": id, "sqlHash": database.SQLHash(in.SQL)})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
	h("POST /api/database-work-orders/{id}/review", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Version int64  `json:"version"`
			Approve bool   `json:"approve"`
			Reason  string `json:"reason"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if !resource.ValidID(r.PathValue("id")) {
			return fault.Invalid
		}
		e := a.securityTx(r, func(c context.Context, u *identity.User) error {
			if e := a.allowed(c, u, "database.approve"); e != nil {
				return e
			}
			service := database.New(a.db)
			v, e := service.Work(c, r.PathValue("id"), true)
			if e != nil {
				return e
			}
			if v.Version != in.Version {
				return fault.Conflict
			}
			if e = a.databaseWorkAccess(c, u.ID, v.AssetID); e != nil {
				return e
			}
			state := "rejected"
			if in.Approve {
				state = "approved"
				if e = a.databaseWorkAccess(c, v.RequesterID, v.AssetID); e != nil {
					return e
				}
				asset, e := service.Asset(c, v.AssetID)
				if e != nil {
					return e
				}
				credential, e := a.vault.Get(c, asset.WriteCredentialID)
				if e != nil {
					return e
				}
				if asset.Version != v.AssetVersion || credential.ActiveVersion != v.CredentialVersion || database.SQLHash(v.SQL) != v.Hash {
					return fault.Conflict
				}
			}
			if e = service.ReviewWork(c, v, u.ID, state, in.Reason); e != nil {
				return e
			}
			if e = a.notifications().EnqueueDatabase(c, v.ID+":"+state, "database."+state, v.ID, v.RequesterID, nil); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "database.work_"+state, requestID(r), sourceIP(r), map[string]string{"id": v.ID, "sqlHash": v.Hash})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("POST /api/database-work-orders/{id}/cancel", func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Version int64 `json:"version"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if !resource.ValidID(r.PathValue("id")) {
			return fault.Invalid
		}
		e := a.securityTx(r, func(c context.Context, u *identity.User) error {
			service := database.New(a.db)
			v, e := service.Work(c, r.PathValue("id"), true)
			if e != nil {
				return e
			}
			if v.RequesterID != u.ID && a.allowed(c, u, "database.approve") != nil {
				return fault.Denied
			}
			if v.Version != in.Version {
				return fault.Conflict
			}
			if e = service.CancelWork(c, v); e != nil {
				return e
			}
			if e = a.notifications().EnqueueDatabase(c, v.ID+":cancelled", "database.cancelled", v.ID, v.RequesterID, nil); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "database.work_cancelled", requestID(r), sourceIP(r), map[string]string{"id": v.ID})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
