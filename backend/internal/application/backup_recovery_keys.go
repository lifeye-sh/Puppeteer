package application

import (
	"context"
	"net/http"
	"puppeteer/backend/internal/modules/backup"
	"puppeteer/backend/internal/modules/identity"
)

// Called before listeners and workers start, only for writable deployments.
func (a *App) InitializeBackupRecovery(ctx context.Context) error {
	return backup.New(a.db).EnsureRecoveryKey(ctx, a.config.VaultKey)
}

func (a *App) routesBackupRecoveryKeys(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := backup.New(a.db)
	h("GET /api/admin/backup/recovery-keys", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "backup.manage"); err != nil {
			return err
		}
		keys, err := s.RecoveryKeys(r.Context())
		if err != nil {
			return err
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, keys)
		return nil
	})
	h("POST /api/admin/backup/recovery-keys", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "backup.manage"); err != nil {
			return err
		}
		var in struct {
			backupProof
			Revision int64 `json:"revision"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var key backup.RecoveryKey
		err := a.withBackupProof(r, in.backupProof, func(ctx context.Context, u *identity.User) error {
			if err := a.allowed(ctx, u, "backup.manage"); err != nil {
				return err
			}
			var err error
			key, err = s.RotateRecoveryKey(ctx, a.config.VaultKey, in.Revision, u.ID)
			if err != nil {
				return err
			}
			return a.audit.Detail(ctx, u.ID, "backup.recovery_key.generated", requestID(r), sourceIP(r), map[string]string{"id": key.ID})
		})
		if err != nil {
			return err
		}
		notifyWork(a.backupWake)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 201, key)
		return nil
	})
	h("POST /api/admin/backup/recovery-keys/{id}/download", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "backup.manage"); err != nil {
			return err
		}
		var in backupProof
		if err := decode(w, r, &in); err != nil {
			return err
		}
		var plain []byte
		defer func() { clear(plain) }()
		err := a.withBackupProof(r, in, func(ctx context.Context, u *identity.User) error {
			if err := a.allowed(ctx, u, "backup.manage"); err != nil {
				return err
			}
			var err error
			plain, err = s.RecoveryIdentity(ctx, a.config.VaultKey, r.PathValue("id"))
			if err != nil {
				return err
			}
			return a.audit.Detail(ctx, u.ID, "backup.recovery_key.downloaded", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if err != nil {
			return err
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		writeJSON(w, 200, map[string]string{"privateKey": string(plain) + "\n", "filename": "recovery-" + r.PathValue("id") + ".agekey"})
		return nil
	})
}
