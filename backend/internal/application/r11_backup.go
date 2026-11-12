package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/identity"
	"regexp"
)

func (a *App) routesR11Backup(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	a.routesBackupDownload(h)
	a.routesBackupDestinations(h)
	a.routesBackupRecipient(h)
	s := backup.New(a.db)
	h("POST /api/admin/backup/tasks/{id}/retry", func(w http.ResponseWriter, r *http.Request) error {
		actor, e := a.require(r, "backup.manage")
		if e != nil {
			return e
		}
		id := r.PathValue("id")
		if e = s.RetryCopies(r.Context(), id); e != nil {
			return e
		}
		if e = a.audit.Detail(r.Context(), actor.ID, "backup.remote_retry", "backup:"+id, "local", map[string]string{"id": id}); e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("GET /api/admin/backup/restores", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		v, e := backup.RestoreHistories(a.config.RestoreDirectory)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"activeRestoreId": a.config.RestoreID, "items": v})
		return nil
	})
	h("GET /api/admin/backup/status", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"available": a.config.BackupDirectory != "" && a.config.DatabaseURL != "", "maintenance": a.maintenance.Load()})
		return nil
	})
	h("GET /api/admin/backup/config", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		v, e := s.Config(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("PUT /api/admin/backup/config", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "backup.manage")
		if e != nil {
			return e
		}
		var in struct {
			backup.Config
			backupProof
			RecipientProofID  string `json:"recipientProofId"`
			RecipientResponse string `json:"recipientResponse"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		// The scheduler gate is intentional while the maintenance worker is being
		// integrated. Never acknowledge an enabled schedule that cannot execute.
		if in.ScheduleEnabled && (a.config.BackupDirectory == "" || a.config.DatabaseURL == "") {
			return problem(409, "backup_scheduler_unavailable", "备份调度尚未就绪，暂不能启用")
		}
		e = a.withBackupProof(r, in.backupProof, func(c context.Context, _ *identity.User) error {
			if e := backup.ValidateConfig(in.Config); e != nil {
				return e
			}
			if e := a.verifyRecipient(c, r, in.Recipient, in.RecipientProofID, in.RecipientResponse); e != nil {
				return e
			}
			if e := s.SaveConfig(c, in.Config); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "backup.config.updated", requestID(r), sourceIP(r), map[string]string{"scope": in.Scope})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("GET /api/admin/backup/tasks", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		v, e := s.Tasks(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("POST /api/admin/backup/tasks", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "backup.manage")
		if e != nil {
			return e
		}
		if a.config.BackupDirectory == "" || a.config.DatabaseURL == "" {
			return problem(409, "backup_runtime_unavailable", "备份运行环境尚未配置")
		}
		var in struct {
			RequestKey     string `json:"requestKey"`
			DrainConfirmed bool   `json:"drainConfirmed"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if !in.DrainConfirmed || !regexp.MustCompile(`^[a-fA-F0-9]{8}(-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`).MatchString(in.RequestKey) {
			return problem(422, "backup_confirmation_required", "请确认备份期间将关闭现有会话并进入维护状态")
		}
		var id string
		e = a.db.Within(r.Context(), func(c context.Context) error {
			var e error
			id, e = s.Request(c, u.ID, in.RequestKey)
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "backup.requested", requestID(r), sourceIP(r), map[string]string{"id": id})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 202, map[string]string{"id": id})
		return nil
	})
	h("GET /api/admin/backup/files", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		v, e := s.Files(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
}
