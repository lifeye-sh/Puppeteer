package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"filippo.io/age"
	"io"
	"net/http"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/identity"
	"os"
	"path/filepath"
	"time"
)

func (a *App) routesBackupDestinations(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	s := backup.New(a.db)
	h("GET /api/admin/backup/destinations", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		v, e := s.Destinations(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("GET /api/admin/backup/copies", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		v, e := s.Copies(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("POST /api/admin/backup/destinations", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.manage"); e != nil {
			return e
		}
		var in struct {
			backup.Destination
			backupProof
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		var id string
		e := a.withBackupProof(r, in.backupProof, func(c context.Context, u *identity.User) error {
			var e error
			id, e = s.SaveDestination(c, in.Destination, a.config.VaultKey)
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "backup.destination.updated", requestID(r), sourceIP(r), map[string]string{"id": id, "kind": in.Kind})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
	h("POST /api/admin/backup/destinations/{id}/test", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "backup.manage")
		if e != nil {
			return e
		}
		if a.config.BackupDirectory == "" {
			return problem(409, "backup_runtime_unavailable", "备份服务尚未就绪")
		}
		d, e := s.DestinationSecret(r.Context(), r.PathValue("id"), a.config.VaultKey)
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		if e = os.MkdirAll(a.config.BackupDirectory, 0700); e != nil {
			return e
		}
		dir, e := os.MkdirTemp(a.config.BackupDirectory, "connection-test-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(dir)
		f, e := os.OpenFile(filepath.Join(dir, "probe.age"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		key, e := age.GenerateX25519Identity()
		if e != nil {
			return e
		}
		encrypted, e := age.Encrypt(f, key.Recipient())
		if e != nil {
			return e
		}
		if _, e = io.WriteString(encrypted, "Puppeteer backup connection verification"); e != nil {
			return e
		}
		if e = encrypted.Close(); e != nil {
			return e
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			return e
		}
		hash := sha256.New()
		if _, e = io.Copy(hash, f); e != nil {
			return e
		}
		f.Close()
		remoteKey := "probe-" + identity.NewToken() + ".age"
		if _, e = backup.CopyRemote(ctx, d, f.Name(), remoteKey, hex.EncodeToString(hash.Sum(nil))); e != nil {
			return problem(422, "backup_destination_test_failed", "远端读写校验失败，请检查地址、认证、证书或主机指纹")
		}
		if e = backup.RemoveRemote(ctx, d, remoteKey); e != nil {
			return problem(422, "backup_destination_cleanup_failed", "读写校验通过，但测试文件清理失败，请检查删除权限")
		}
		if e = a.audit.Detail(r.Context(), u.ID, "backup.destination.tested", requestID(r), sourceIP(r), map[string]string{"id": d.ID}); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"state": "verified"})
		return nil
	})
}
