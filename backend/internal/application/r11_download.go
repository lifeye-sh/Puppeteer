package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"os"
	"path/filepath"
	"time"
)

func (a *App) routesBackupDownload(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("POST /api/admin/backup/files/{id}/export-ticket", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "backup.export")
		if e != nil {
			return e
		}
		var in struct {
			Password string `json:"password"`
			TOTP     string `json:"totp"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if len(in.Password) > 1024 {
			return fault.Invalid
		}
		release, e := a.hashPermit(r)
		if e != nil {
			return e
		}
		defer release()
		ticket := identity.NewToken()
		failed := false
		e = a.securityTx(r, func(c context.Context, current *identity.User) error {
			if current.ID != u.ID {
				return fault.Denied
			}
			if e := a.identity.CheckLock(c, u.Username); e != nil {
				return e
			}
			verified, e := a.identity.Verify(c, u.Username, in.Password)
			if e != nil {
				return e
			}
			if verified == nil {
				failed = true
				return a.identity.FailedPassword(c, u.Username)
			}
			enabled, _, e := a.identity.MFAStatus(c, u.ID)
			if e != nil {
				return e
			}
			if enabled {
				if e = a.identity.ConsumeTOTP(c, u.ID, in.TOTP); e != nil {
					failed = true
					return a.identity.FailedPassword(c, u.Username)
				}
			}
			if e = a.identity.Unlock(c, u.Username); e != nil {
				return e
			}
			var available bool
			if e = a.db.Row(c, `SELECT state='available' FROM backup_files WHERE id=$1`, r.PathValue("id")).Scan(&available); e != nil {
				return e
			}
			if !available {
				return problem(404, "backup_not_found", "备份文件不存在")
			}
			if _, e = a.db.Exec(c, `INSERT INTO backup_export_tickets(token_hash,session_hash,file_id) VALUES($1,$2,$3)`, identity.Digest(ticket), identity.Digest(token(r)), r.PathValue("id")); e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "backup.export_authorized", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		if failed {
			return fault.Proof
		}
		writeJSON(w, 200, map[string]string{"ticket": ticket})
		return nil
	})
	h("GET /api/admin/backup/files/{id}/download", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "backup.export")
		if e != nil {
			return e
		}
		ticket := r.URL.Query().Get("ticket")
		if len(ticket) < 32 || len(ticket) > 256 {
			return fault.Proof
		}
		e = a.db.Within(r.Context(), func(c context.Context) error {
			tag, e := a.db.Exec(c, `UPDATE backup_export_tickets SET used_at=now() WHERE token_hash=$1 AND session_hash=$2 AND file_id=$3 AND expires_at>now() AND used_at IS NULL`, identity.Digest(ticket), identity.Digest(token(r)), r.PathValue("id"))
			if e != nil {
				return e
			}
			if tag.RowsAffected() != 1 {
				return fault.Proof
			}
			return nil
		})
		if e != nil {
			return e
		}
		return a.db.Within(r.Context(), func(c context.Context) error {
			var key, expected string
			var size int64
			var created time.Time
			if e := a.db.Row(c, `SELECT storage_key,size,created_at,sha256 FROM backup_files WHERE id=$1 AND state='available' FOR SHARE`, r.PathValue("id")).Scan(&key, &size, &created, &expected); e != nil {
				return e
			}
			if key != r.PathValue("id")+".ntbackup.age" || filepath.Base(key) != key || a.config.BackupDirectory == "" {
				return problem(404, "backup_not_found", "备份文件不存在")
			}
			f, e := os.Open(filepath.Join(a.config.BackupDirectory, key))
			if e != nil {
				return problem(409, "backup_missing", "备份文件不可用，请检查存储")
			}
			defer f.Close()
			info, e := f.Stat()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || info.Size() != size {
				return problem(409, "backup_corrupt", "备份文件不完整")
			}
			hash := sha256.New()
			if _, e = io.Copy(hash, f); e != nil {
				return e
			}
			if hex.EncodeToString(hash.Sum(nil)) != expected {
				return problem(409, "backup_corrupt", "备份文件完整性校验失败")
			}
			if _, e = f.Seek(0, io.SeekStart); e != nil {
				return e
			}
			if e = a.audit.Detail(c, u.ID, "backup.downloaded", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")}); e != nil {
				return e
			}
			w.Header().Set("Content-Disposition", `attachment; filename="`+key+`"`)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Hour))
			http.ServeContent(w, r, key, created, f)
			return nil
		})
	})
}
