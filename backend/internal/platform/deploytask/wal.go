package deploytask

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed wal-build/*
var walBuild embed.FS

type walRecipe struct {
	Image   json.RawMessage `json:"image"`
	Command json.RawMessage `json:"command,omitempty"`
	Enabled bool            `json:"enabled"`
}

func walImage() string {
	raw, _ := walBuild.ReadFile("wal-build/Dockerfile")
	conf, _ := walBuild.ReadFile("wal-build/pgbackrest.conf")
	wrapper, _ := walBuild.ReadFile("wal-build/pgbackrest-file")
	sum := sha256.Sum256(append(append(raw, conf...), wrapper...))
	return fmt.Sprintf("puppeteer/managed-postgres-wal:%x", sum[:12])
}
func walEnabled(raw []byte) bool {
	var doc struct {
		WAL walRecipe `json:"x-puppeteer-wal"`
	}
	return json.Unmarshal(raw, &doc) == nil && doc.WAL.Enabled
}

func (d *HostDriver) walPreflight(ctx context.Context, raw []byte, r Request) error {
	if _, e := d.walPlan(ctx, raw, r); e != nil {
		return e
	}
	version, e := d.Executor.run(ctx, 10*time.Second, "check PostgreSQL major", "exec", "-T", "--user", "postgres", "postgres", "psql", "-U", "puppeteer", "-d", "puppeteer", "-Atc", "SHOW server_version_num")
	n, parse := strconv.Atoi(strings.TrimSpace(string(version)))
	if e != nil || parse != nil || n < 180000 || n >= 190000 {
		return errors.New("WAL 自动化仅支持当前健康的 PostgreSQL 18 标准部署")
	}
	if !r.Enabled {
		return nil
	}
	if e := d.checkWALGuard(ctx); e != nil {
		return e
	}
	rawSize, e := d.Executor.run(ctx, 10*time.Second, "check backup capacity", "exec", "-T", "--user", "postgres", "postgres", "sh", "-c", "psql -U puppeteer -d puppeteer -Atc 'SELECT sum(pg_database_size(oid)) FROM pg_database'; df -Pk /var/lib/postgresql | tail -1 | awk '{print $4}'")
	sizes := strings.Fields(string(rawSize))
	if e != nil || len(sizes) != 2 {
		return errors.New("无法读取备份及隔离恢复所需空间")
	}
	dbSize, e1 := strconv.ParseInt(sizes[0], 10, 64)
	available, e2 := strconv.ParseInt(sizes[1], 10, 64)
	if e1 != nil || e2 != nil || dbSize < 0 || available < 0 || available*1024 < dbSize*3+(512<<20) {
		return errors.New("Docker 数据盘空闲空间不足以容纳基础备份和隔离恢复，请至少保留数据库大小三倍及 512 MiB")
	}
	return nil
}

func (d *HostDriver) walPlan(ctx context.Context, raw []byte, r Request) (ComposePlan, error) {
	var doc map[string]json.RawMessage
	var model composeModel
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &model) != nil {
		return ComposePlan{}, errors.New("WAL Compose 配置无效")
	}
	pg := model.Services["postgres"]
	if pg == nil {
		return ComposePlan{}, errors.New("未找到标准 PostgreSQL 服务")
	}
	var env map[string]string
	json.Unmarshal(pg["environment"], &env)
	if env["POSTGRES_USER"] != "puppeteer" || env["POSTGRES_DB"] != "puppeteer" {
		return ComposePlan{}, errors.New("非标准 PostgreSQL 数据库配置，不能自动接管")
	}
	var mounts []map[string]json.RawMessage
	if json.Unmarshal(pg["volumes"], &mounts) != nil {
		return ComposePlan{}, errors.New("PostgreSQL 数据卷不可读取")
	}
	found := false
	for _, m := range mounts {
		var target, kind string
		json.Unmarshal(m["target"], &target)
		json.Unmarshal(m["type"], &kind)
		if target == "/var/lib/postgresql" && kind == "volume" {
			found = true
		}
		if target == "/var/lib/pgbackrest" {
			var source string
			json.Unmarshal(m["source"], &source)
			if source != "puppeteer_managed_wal" {
				return ComposePlan{}, errors.New("存在外部 WAL 仓库挂载，拒绝接管或删除")
			}
		}
	}
	if !found {
		return ComposePlan{}, errors.New("WAL 自动化需要标准命名 PostgreSQL 数据卷")
	}
	var recipe walRecipe
	if value := doc["x-puppeteer-wal"]; value != nil {
		if json.Unmarshal(value, &recipe) != nil || len(recipe.Image) == 0 {
			return ComposePlan{}, errors.New("WAL 恢复配方损坏")
		}
	} else {
		var image string
		json.Unmarshal(pg["image"], &image)
		var budgetVersion int
		_ = json.Unmarshal(doc["x-puppeteer-database-budget"], &budgetVersion)
		plain, budgetErr := databaseCommand(pg["command"], budgetVersion == 1)
		if !strings.HasPrefix(image, "postgres:18-") || budgetErr != nil || len(plain) != 1 {
			return ComposePlan{}, errors.New("自定义 PostgreSQL 镜像或启动参数尚未接管")
		}
		recipe = walRecipe{Image: pg["image"], Command: pg["command"]}
	}
	// Preserve all data mounts; only the managed repository is added/removed.
	out := []map[string]json.RawMessage{}
	for _, m := range mounts {
		var target string
		json.Unmarshal(m["target"], &target)
		if target != "/var/lib/pgbackrest" {
			out = append(out, m)
		}
	}
	var secrets []map[string]json.RawMessage
	if value := pg["secrets"]; value != nil && json.Unmarshal(value, &secrets) != nil {
		return ComposePlan{}, errors.New("PostgreSQL secret 配置无效")
	}
	kept := []map[string]json.RawMessage{}
	for _, s := range secrets {
		var source string
		json.Unmarshal(s["source"], &source)
		if source != "puppeteer_managed_wal_key" {
			kept = append(kept, s)
		}
	}
	if r.Enabled {
		pg["image"], _ = json.Marshal(walImage())
		delete(pg, "build")
		pg["command"], _ = json.Marshal([]string{"postgres", "-c", "archive_mode=on", "-c", "archive_timeout=60", "-c", "archive_command=pgbackrest-file --stanza=puppeteer archive-push %p"})
		out = append(out, map[string]json.RawMessage{"type": json.RawMessage(`"volume"`), "source": json.RawMessage(`"puppeteer_managed_wal"`), "target": json.RawMessage(`"/var/lib/pgbackrest"`)})
		kept = append(kept, map[string]json.RawMessage{"source": json.RawMessage(`"puppeteer_managed_wal_key"`), "target": json.RawMessage(`"wal_cipher_pass"`)})
		var volumes map[string]json.RawMessage
		json.Unmarshal(doc["volumes"], &volumes)
		if volumes == nil {
			volumes = map[string]json.RawMessage{}
		}
		volumes["puppeteer_managed_wal"], _ = json.Marshal(map[string]string{"name": d.Executor.Project + "_managed_wal"})
		doc["volumes"], _ = json.Marshal(volumes)
		var globalSecrets map[string]json.RawMessage
		json.Unmarshal(doc["secrets"], &globalSecrets)
		if globalSecrets == nil {
			globalSecrets = map[string]json.RawMessage{}
		}
		globalSecrets["puppeteer_managed_wal_key"], _ = json.Marshal(map[string]string{"file": filepath.Join(d.StateDirectory, "wal_cipher_pass")})
		doc["secrets"], _ = json.Marshal(globalSecrets)
	} else {
		pg["image"] = recipe.Image
		if len(recipe.Command) == 0 || string(recipe.Command) == "null" {
			delete(pg, "command")
		} else {
			pg["command"] = recipe.Command
		}
	}
	var serverEnv map[string]string
	_ = json.Unmarshal(model.Services["server"]["environment"], &serverEnv)
	if err := applyDatabaseBudget(doc, model, serverEnv["NT_RESOURCE_PROFILE"]); err != nil {
		return ComposePlan{}, err
	}
	pg["volumes"], _ = json.Marshal(out)
	pg["secrets"], _ = json.Marshal(kept)
	recipe.Enabled = r.Enabled
	doc["x-puppeteer-wal"], _ = json.Marshal(recipe)
	doc["services"], _ = json.Marshal(model.Services)
	document, e := json.Marshal(doc)
	return ComposePlan{Document: document}, e
}

func (d *HostDriver) walPrepare(ctx context.Context, raw []byte, r Request) error {
	if e := d.walPreflight(ctx, raw, r); e != nil {
		return e
	}
	if !r.Enabled {
		return nil
	}
	if e := d.opsStage("wal_prepare"); e != nil {
		return e
	}
	path := filepath.Join(d.StateDirectory, "wal_cipher_pass")
	if info, e := os.Lstat(path); e == nil {
		key, e := os.ReadFile(path)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || len(strings.TrimSpace(string(key))) != 64 {
			return errors.New("已有 WAL 加密密钥无效；不会自动替换")
		}
	} else if os.IsNotExist(e) {
		key := make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		if e = atomicPrivate(path, []byte(hex.EncodeToString(key)+"\n")); e != nil {
			return e
		}
		if e = os.Chown(path, 70, 70); e != nil {
			return e
		}
		if e = os.Chmod(path, 0400); e != nil {
			return e
		}
	} else {
		return errors.New("WAL 加密密钥无法检查")
	}
	build := filepath.Join(d.StateDirectory, "wal-build")
	if e := os.MkdirAll(build, 0700); e != nil {
		return e
	}
	for _, name := range []string{"Dockerfile", "pgbackrest.conf", "pgbackrest-file"} {
		raw, e := walBuild.ReadFile("wal-build/" + name)
		if e != nil {
			return e
		}
		if e = atomicPrivate(filepath.Join(build, name), []byte(strings.ReplaceAll(string(raw), "\r\n", "\n"))); e != nil {
			return e
		}
	}
	if e := d.opsStage("wal_image"); e != nil {
		return e
	}
	_, e := d.Executor.execute(ctx, 10*time.Minute, "build pinned WAL image", "build", "--tag", walImage(), "--file", filepath.Join(build, "Dockerfile"), build)
	return e
}
func (d *HostDriver) walRestart(ctx context.Context) error {
	if e := d.Executor.Validate(ctx); e != nil {
		return e
	}
	_, e := d.Executor.run(ctx, 4*time.Minute, "restart PostgreSQL", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "180", "postgres")
	return e
}
func (d *HostDriver) walCommand(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	tail := []string{"exec", "-T", "--user", "postgres", "postgres", "pgbackrest-file", "--stanza=puppeteer"}
	base, e := d.Executor.args()
	if e != nil {
		return nil, e
	}
	return d.walExecute(ctx, timeout, append(base, append(tail, args...)...)...)
}

func (d *HostDriver) walExecute(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, d.Executor.Docker, args...)
	command.Dir = d.Executor.Directory
	command.WaitDelay = 2 * time.Second
	out := &limitedOutput{limit: 1 << 20}
	command.Stdout = out
	command.Stderr = out
	e := command.Run()
	if e != nil {
		atomicPrivate(filepath.Join(d.StateDirectory, "wal-last-error.log"), out.Bytes())
		return nil, errors.New("WAL 归档或备份命令失败；受保护诊断位于 deployd-state/wal-last-error.log")
	}
	if out.exceeded {
		return nil, errors.New("WAL diagnostic output exceeded limit")
	}
	return out.Bytes(), nil
}
func (d *HostDriver) walApply(ctx context.Context, r Request) error {
	if e := d.opsStage("wal_restart"); e != nil {
		return e
	}
	if e := d.walRestart(ctx); e != nil {
		return e
	}
	if !r.Enabled {
		return nil
	}
	if e := d.opsStage("wal_stanza"); e != nil {
		return e
	}
	if _, e := d.walCommand(ctx, 2*time.Minute, "stanza-create"); e != nil {
		return e
	}
	if _, e := d.walCommand(ctx, 3*time.Minute, "check"); e != nil {
		return e
	}
	if e := d.opsStage("wal_backup"); e != nil {
		return e
	}
	_, e := d.walCommand(ctx, 30*time.Minute, "--type=full", "--start-fast", "backup")
	return e
}
func (d *HostDriver) walVerify(ctx context.Context, r Request) error {
	if !r.Enabled {
		mode, e := d.Executor.run(ctx, 10*time.Second, "verify archive disabled", "exec", "-T", "--user", "postgres", "postgres", "psql", "-U", "puppeteer", "-d", "puppeteer", "-Atc", "SHOW archive_mode")
		if e != nil || strings.TrimSpace(string(mode)) != "off" {
			return errors.New("WAL 归档尚未确认停用")
		}
		return d.saveOpsState(r, "stopped", "归档与定时备份已停用；原 WAL 仓库、密钥及历史备份保留")
	}
	if e := d.opsStage("wal_archive"); e != nil {
		return e
	}
	token := make([]byte, 8)
	if _, e := rand.Read(token); e != nil {
		return e
	}
	point := "puppeteer_deploy_" + hex.EncodeToString(token)
	if _, e := d.Executor.run(ctx, 10*time.Second, "create recovery verification point", "exec", "-T", "--user", "postgres", "postgres", "psql", "-U", "puppeteer", "-d", "puppeteer", "-Atc", "SELECT pg_create_restore_point('"+point+"')"); e != nil {
		return e
	}
	if _, e := d.walCommand(ctx, 3*time.Minute, "check"); e != nil {
		return e
	}
	if e := d.opsStage("wal_restore"); e != nil {
		return e
	}
	if e := d.walDrill(ctx, point); e != nil {
		return e
	}
	schedule := walSchedule{Full: time.Now().UTC(), Backup: time.Now().UTC(), Check: time.Now().UTC()}
	if e := d.saveWALSchedule(schedule); e != nil {
		return e
	}
	return d.saveOpsState(r, "recoverable", "首份加密备份、连续归档及隔离命名恢复点演练通过；后台调度已接管。本地仓库不是异地副本")
}

func (d *HostDriver) walDrill(ctx context.Context, point string) (err error) {
	if err = d.RecoverWALDrills(ctx); err != nil {
		return err
	}
	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		return err
	}
	name := d.Executor.Project + "-wal-drill-" + hex.EncodeToString(suffix)
	volume := name + "-data"
	if _, err = d.Executor.execute(ctx, 15*time.Second, "create isolated restore volume", "volume", "create", "--label", "io.puppeteer.wal-drill="+d.Executor.Project, volume); err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		d.Executor.execute(c, 15*time.Second, "remove isolated restore container", "rm", "-f", name)
		if _, e := d.Executor.execute(c, 15*time.Second, "remove isolated restore volume", "volume", "rm", volume); e != nil && err == nil {
			err = errors.New("隔离恢复已结束，但演练卷清理失败，请查看宿主任务")
		}
	}()
	script := `set -eu
 chown postgres:postgres /restore
 exec gosu postgres sh -c '
 set -eu
 pgbackrest-file --stanza=puppeteer --pg1-path=/restore/pg --type=name --target="$1" --target-action=promote --archive-mode=off --log-path=/tmp restore
 trap '\''pg_ctl -D /restore/pg -m immediate stop >/dev/null 2>&1 || true'\'' EXIT
 pg_ctl -D /restore/pg -l /tmp/restore.log -o "-c listen_addresses= -c unix_socket_directories=/tmp -c archive_mode=off -p 55432" -w -t 120 start || { cat /tmp/restore.log; exit 1; }
 # Read-only startup can precede replay of the named target and promotion.
 ready=false
 for attempt in $(seq 1 120); do
   if test "$(psql -h /tmp -p 55432 -U puppeteer -d puppeteer -Atc "SELECT NOT pg_is_in_recovery()" 2>/dev/null)" = t; then ready=true; break; fi
   sleep 1
 done
 test "$ready" = true || { cat /tmp/restore.log; exit 1; }
 test "$(psql -h /tmp -p 55432 -U puppeteer -d puppeteer -Atc "SELECT count(*) FROM instance_state WHERE singleton=1")" = 1
 ' sh "$1"`
	_, err = d.walExecute(ctx, 15*time.Minute, "run", "--rm", "--name", name, "--label", "io.puppeteer.wal-drill="+d.Executor.Project, "--network", "none", "--memory", "2g", "--pids-limit", "256", "--mount", "type=volume,source="+volume+",target=/restore", "--mount", "type=volume,source="+d.Executor.Project+"_managed_wal,target=/var/lib/pgbackrest,readonly", "--mount", "type=bind,source="+filepath.Join(d.StateDirectory, "wal_cipher_pass")+",target=/run/secrets/wal_cipher_pass,readonly", "--entrypoint", "sh", walImage(), "-c", script, "sh", point)
	return err
}

type walSchedule struct {
	Full   time.Time `json:"full"`
	Backup time.Time `json:"backup"`
	Check  time.Time `json:"check"`
	Error  string    `json:"error,omitempty"`
}

func (d *HostDriver) saveWALSchedule(s walSchedule) error {
	raw, _ := json.Marshal(s)
	return atomicPrivate(filepath.Join(d.StateDirectory, "wal-schedule.json"), raw)
}

// Called only while holding the same engine gate as interactive deployments.
func (d *HostDriver) maintainWAL(ctx context.Context) error {
	raw, e := os.ReadFile(d.Executor.Files[0])
	if e != nil || !walEnabled(raw) {
		return e
	}
	raw, e = os.ReadFile(filepath.Join(d.StateDirectory, "wal-schedule.json"))
	if e != nil {
		return e
	}
	var s walSchedule
	if json.Unmarshal(raw, &s) != nil {
		return errors.New("WAL schedule unreadable")
	}
	if e = d.checkWALGuard(ctx); e != nil {
		s.Error = e.Error()
		return d.saveWALSchedule(s)
	}
	now := time.Now().UTC()
	operation := ""
	if now.Sub(s.Full) >= 7*24*time.Hour {
		operation = "full"
	} else if now.Sub(s.Backup) >= 24*time.Hour {
		operation = "diff"
	}
	if operation != "" {
		_, e = d.walCommand(ctx, 30*time.Minute, "--type="+operation, "--start-fast", "backup")
		if e == nil {
			s.Backup = now
			if operation == "full" {
				s.Full = now
			}
		}
	} else if now.Sub(s.Check) >= 5*time.Minute {
		_, e = d.walCommand(ctx, 3*time.Minute, "check")
		if e == nil {
			s.Check = now
		}
	} else {
		s.Error = ""
		return d.saveWALSchedule(s)
	}
	s.Error = ""
	if e != nil {
		s.Error = "WAL 定时备份或归档检查失败；保留原备份，将自动重试"
	}
	return d.saveWALSchedule(s)
}

func (d *HostDriver) RunMaintenance(ctx context.Context, e *Engine) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.Maintain(ctx, d.maintainWAL)
		}
	}
}
