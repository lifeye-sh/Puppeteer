package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"puppeteer/backend/internal/modules/backup"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/secretbox"
)

func (a *App) verifyBackupDependency(ctx context.Context, t configstate.Task, d backup.Destination, report func(configstate.Check) bool) {
	check := func(code string, err error, good, bad string) bool {
		state, summary := "ready", good
		if err != nil {
			state, summary = "blocked", bad
		}
		return report(configstate.Check{Code: code, State: state, Summary: summary, Scope: "explicit-backup-verification", CheckedAt: time.Now().UTC()}) && err == nil
	}
	permission := func() error {
		var user identity.User
		err := a.db.Row(ctx, `SELECT id,type FROM users WHERE id=$1 AND status='enabled' AND deleted_at IS NULL`, t.Actor).Scan(&user.ID, &user.Type)
		if err != nil || user.Type != "admin" {
			return errors.New("actor_unavailable")
		}
		if err = a.allowed(ctx, &user, "identity.manage"); err != nil {
			return err
		}
		return a.allowed(ctx, &user, "backup.manage")
	}
	if !check("backup_authorization", permission(), "发起人的当前备份管理权限已复核", "发起人已停用或权限变化，未开始远端验证") {
		return
	}
	// Own only these two unpredictable object names. Keep the exact credentials
	// encrypted until both objects have been removed, even after resource edits.
	key := "probe-" + t.ID + ".age"
	raw, err := json.Marshal(d)
	if err != nil {
		check("backup_prepare", err, "", "无法准备隔离验证对象")
		return
	}
	defer clear(raw)
	sealed, err := secretbox.Seal(a.config.VaultKey, raw, "dependency-artifact:"+t.ID)
	if err == nil {
		_, err = a.db.Exec(ctx, `INSERT INTO dependency_probe_artifacts(task_id,resource_id,sealed,remote_key) SELECT $1,$2,$3,$4 WHERE NOT EXISTS(SELECT 1 FROM dependency_probe_artifacts WHERE resource_id=$2)`, t.ID, d.ID, sealed, key)
	}
	var registered bool
	if err == nil {
		err = a.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM dependency_probe_artifacts WHERE task_id=$1)`, t.ID).Scan(&registered)
		if !registered {
			err = errors.New("cleanup_pending")
		}
	}
	if !check("backup_prepare", err, "已登记隔离测试对象及重启清理信息", "目标仍有待清理测试对象或无法持久登记，请先恢复清理") {
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := a.cleanupDependencyArtifact(cleanup, t.ID)
		check("backup_cleanup", err, "隔离测试对象及临时上传对象已清理", "测试对象清理失败；后台将持续重试，不能判定目标验证通过")
	}()
	if err = os.MkdirAll(a.config.BackupDirectory, 0700); err != nil {
		check("backup_prepare_file", err, "", "备份目录不可写")
		return
	}
	dir, err := os.MkdirTemp(a.config.BackupDirectory, "dependency-test-")
	if err != nil {
		check("backup_prepare_file", err, "", "无法创建隔离测试目录")
		return
	}
	defer os.RemoveAll(dir)
	f, err := os.OpenFile(filepath.Join(dir, "probe.age"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		check("backup_prepare_file", err, "", "无法创建隔离测试文件")
		return
	}
	defer f.Close()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		check("backup_prepare_file", err, "", "无法生成临时加密身份")
		return
	}
	w, err := age.Encrypt(f, identity.Recipient())
	if err == nil {
		_, err = io.WriteString(w, "Puppeteer isolated destination verification")
		if closeErr := w.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	hash := sha256.New()
	if err == nil {
		_, err = io.Copy(hash, f)
	}
	f.Close()
	if !check("backup_prepare_file", err, "已生成仅含固定测试文本的加密对象", "生成加密测试对象失败") {
		return
	}
	if !check("backup_write_authorization", permission(), "远端写入前权限复核通过", "权限已撤销，保留清理流程并停止写入") {
		return
	}
	_, err = backup.CopyRemote(ctx, d, f.Name(), key, hex.EncodeToString(hash.Sum(nil)))
	check("backup_roundtrip", err, "远端上传、发布和 SHA-256 回读验证通过", "远端写入、发布或摘要回读失败，请检查路径权限与服务器能力")
}

func (a *App) cleanupDependencyArtifact(ctx context.Context, id string) error {
	return a.db.Within(ctx, func(ctx context.Context) error {
		var sealed []byte
		var key string
		if err := a.db.Row(ctx, `SELECT sealed,remote_key FROM dependency_probe_artifacts WHERE task_id=$1 FOR UPDATE`, id).Scan(&sealed, &key); err != nil {
			return err
		}
		raw, err := secretbox.Open(a.config.VaultKey, sealed, "dependency-artifact:"+id)
		if err != nil {
			return errors.New("cleanup_credentials_unavailable")
		}
		defer clear(raw)
		var d backup.Destination
		if json.Unmarshal(raw, &d) != nil || key != "probe-"+id+".age" {
			return errors.New("invalid_owned_artifact")
		}
		first := backup.RemoveRemote(ctx, d, key+".partial")
		second := backup.RemoveRemote(ctx, d, key)
		if first != nil || second != nil {
			return errors.New("cleanup_pending")
		}
		_, err = a.db.Exec(ctx, `DELETE FROM dependency_probe_artifacts WHERE task_id=$1`, id)
		return err
	})
}

func (a *App) RecoverDependencyArtifacts(ctx context.Context) {
	rows, err := a.db.Rows(ctx, `SELECT a.task_id FROM dependency_probe_artifacts a JOIN configuration_checks t ON t.id=a.task_id WHERE t.deadline<now() OR t.state<>'running' ORDER BY a.created_at LIMIT 20`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		_ = a.cleanupDependencyArtifact(c, id)
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
}

func (a *App) RunDependencyCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !a.maintenance.Load() {
				a.RecoverDependencyArtifacts(ctx)
			}
		}
	}
}
