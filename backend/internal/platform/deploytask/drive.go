package deploytask

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const driveHelperTarget = "/usr/local/bin/puppeteer-drive"
const driveStorageTarget = "/var/lib/puppeteer/storage"

type driveEnrollment struct {
	Version int                        `json:"version"`
	Recipe  map[string]json.RawMessage `json:"recipe"`
}

func driveDesired(raw []byte) bool {
	var d map[string]json.RawMessage
	var g graphicsEnrollment
	json.Unmarshal(raw, &d)
	json.Unmarshal(d["x-puppeteer-graphics"], &g)
	return g.DriveEnabled == "true"
}
func planDrive(raw []byte, r Request, helper, profile string) (ComposePlan, error) {
	if err := Validate(r); err != nil {
		return ComposePlan{}, err
	}
	graphics := graphicsEnabled(raw)
	if r.Enabled && !graphics {
		return ComposePlan{}, errors.New("请先启用图形组件，再启用 RDP 共享盘")
	}
	base, err := planGraphics(raw, Request{Protocol: "graphics", Enabled: graphics})
	if err != nil {
		return ComposePlan{}, err
	}
	var doc map[string]json.RawMessage
	var m composeModel
	var enrollment driveEnrollment
	json.Unmarshal(base.Document, &doc)
	json.Unmarshal(base.Document, &m)
	if json.Unmarshal(doc["x-puppeteer-drive"], &enrollment) != nil || enrollment.Version != 1 || enrollment.Recipe == nil {
		return ComposePlan{}, errors.New("共享盘配方未接管；需使用匹配的安装升级版本")
	}
	recipe := enrollment.Recipe
	var devices []struct{ Source, Target string }
	json.Unmarshal(recipe["devices"], &devices)
	if len(devices) != 1 || devices[0].Source != "/dev/fuse" || devices[0].Target != "/dev/fuse" {
		return ComposePlan{}, errors.New("共享盘仅允许 /dev/fuse 设备")
	}
	var mounts []struct {
		Type, Source, Target string
		ReadOnly             bool `json:"read_only"`
	}
	json.Unmarshal(recipe["volumes"], &mounts)
	if len(mounts) != 1 || mounts[0].Type != "volume" || mounts[0].Target != driveStorageTarget || !mounts[0].ReadOnly {
		return ComposePlan{}, errors.New("共享盘必须只读挂载专用 storageconfig 卷")
	}
	var serverMounts []struct {
		Type, Source, Target string
		ReadOnly             bool `json:"read_only"`
	}
	json.Unmarshal(m.Services["server"]["volumes"], &serverMounts)
	shared := false
	for _, v := range serverMounts {
		if v.Type == "volume" && v.Source == mounts[0].Source && v.Target == driveStorageTarget && !v.ReadOnly {
			shared = true
		}
	}
	var volumes map[string]struct{ External bool }
	json.Unmarshal(doc["volumes"], &volumes)
	v, exists := volumes[mounts[0].Source]
	if !shared || !exists || v.External {
		return ComposePlan{}, errors.New("共享盘与应用必须使用同一项目专用存储配置卷，应用端可写")
	}
	var networks map[string]json.RawMessage
	json.Unmarshal(recipe["networks"], &networks)
	if len(networks) != 2 || networks["graphics"] == nil || networks["database"] == nil {
		return ComposePlan{}, errors.New("共享盘配方仅支持 database 与 graphics 网络")
	}
	var env map[string]string
	json.Unmarshal(recipe["environment"], &env)
	if env["NT_STORAGE_PROFILE"] != driveStorageTarget+"/active.json" || (env["NT_DATABASE_URL_FILE"] == "" && env["NT_DATABASE_URL"] == "") {
		return ComposePlan{}, errors.New("共享盘数据库或存储配置路径缺失")
	}
	var caps []string
	json.Unmarshal(recipe["cap_add"], &caps)
	allowed := map[string]bool{"SYS_ADMIN": true, "SETUID": true, "SETGID": true, "DAC_OVERRIDE": true}
	for _, c := range caps {
		if !allowed[c] {
			return ComposePlan{}, errors.New("自定义共享盘权限不支持自动管理")
		}
	}

	// Reject drift outside the enrolled recipe instead of deleting custom mounts/commands.
	var currentMounts []struct {
		Type, Source, Target string
		ReadOnly             bool `json:"read_only"`
	}
	json.Unmarshal(m.Services["guacd"]["volumes"], &currentMounts)
	for _, v := range currentMounts {
		storageMount := v.Type == "volume" && v.Source == mounts[0].Source && v.Target == driveStorageTarget && v.ReadOnly
		helperMount := v.Type == "bind" && v.Source == helper && v.Target == driveHelperTarget && v.ReadOnly
		if !storageMount && !helperMount {
			return ComposePlan{}, errors.New("图形容器存在未接管挂载，拒绝替换")
		}
	}
	var currentDevices []struct{ Source, Target string }
	json.Unmarshal(m.Services["guacd"]["devices"], &currentDevices)
	for _, v := range currentDevices {
		if v.Source != "/dev/fuse" || v.Target != "/dev/fuse" {
			return ComposePlan{}, errors.New("图形容器存在未接管设备")
		}
	}
	var currentCaps []string
	json.Unmarshal(m.Services["guacd"]["cap_add"], &currentCaps)
	for _, c := range currentCaps {
		if !allowed[c] {
			return ComposePlan{}, errors.New("图形容器存在未接管权限")
		}
	}
	var entry []string
	json.Unmarshal(m.Services["guacd"]["entrypoint"], &entry)
	if len(entry) > 0 && !(len(entry) == 1 && (entry[0] == "/usr/local/bin/nt-rdp-storage" || entry[0] == "/opt/guacamole/sbin/guacd")) && !(len(entry) == 2 && entry[0] == driveHelperTarget && entry[1] == "rdp-storage") {
		return ComposePlan{}, errors.New("自定义图形启动程序不支持自动替换")
	}
	var privileged bool
	json.Unmarshal(m.Services["guacd"]["privileged"], &privileged)
	if privileged {
		return ComposePlan{}, errors.New("不接管 privileged 图形容器")
	}
	var security []string
	json.Unmarshal(m.Services["guacd"]["security_opt"], &security)
	for _, value := range security {
		if value != "no-new-privileges:true" && value != "no-new-privileges" && value != "apparmor=puppeteer-rdp-storage" && value != "apparmor:"+profile && value != "apparmor="+profile && !strings.HasPrefix(value, "apparmor=puppeteer-rdp-storage-") {
			return ComposePlan{}, errors.New("自定义图形安全策略不支持自动替换")
		}
	}
	g := m.Services["guacd"]
	set := func(key string, value any) { g[key], _ = json.Marshal(value) }
	set("cap_drop", []string{"ALL"})
	set("read_only", true)
	if r.Enabled {
		if !filepath.IsAbs(helper) {
			return ComposePlan{}, errors.New("共享盘执行程序路径无效")
		}
		for _, key := range []string{"environment", "secrets", "networks"} {
			if recipe[key] != nil {
				g[key] = recipe[key]
			} else {
				delete(g, key)
			}
		}
		set("devices", []any{map[string]any{"source": "/dev/fuse", "target": "/dev/fuse", "permissions": "rwm"}})
		set("cap_add", []string{"SYS_ADMIN", "SETUID", "SETGID", "DAC_OVERRIDE"})
		set("user", "0:0")
		set("entrypoint", []string{driveHelperTarget, "rdp-storage"})
		set("command", []string{})
		set("volumes", []any{map[string]any{"type": "volume", "source": mounts[0].Source, "target": driveStorageTarget, "read_only": true}, map[string]any{"type": "bind", "source": helper, "target": driveHelperTarget, "read_only": true}})
		opts := []string{"no-new-privileges:true"}
		if profile != "" {
			opts = append(opts, "apparmor="+profile)
		}
		set("security_opt", opts)
		set("healthcheck", map[string]any{"test": []string{"CMD", driveHelperTarget, "rdp-storage", "health"}, "interval": "5s", "timeout": "3s", "retries": 20})
	} else {
		for _, key := range []string{"devices", "cap_add", "volumes", "secrets", "environment"} {
			delete(g, key)
		}
		set("user", "1000:1000")
		set("entrypoint", []string{"/opt/guacamole/sbin/guacd"})
		set("command", []string{"-f", "-b", "0.0.0.0"})
		set("networks", map[string]any{"graphics": nil})
		set("security_opt", []string{"no-new-privileges:true"})
		set("healthcheck", map[string]any{"test": []string{"CMD-SHELL", "nc -z 127.0.0.1 4822"}, "interval": "5s", "timeout": "3s", "retries": 20})
	}
	var serverEnv map[string]string
	json.Unmarshal(m.Services["server"]["environment"], &serverEnv)
	flag := "false"
	if r.Enabled {
		flag = "true"
	}
	serverEnv["NT_RDP_DRIVE_ENABLED"] = flag
	serverEnv["NT_DEPLOY_MANAGED_RDP_DRIVE"] = flag
	m.Services["server"]["environment"], _ = json.Marshal(serverEnv)
	var meta graphicsEnrollment
	json.Unmarshal(doc["x-puppeteer-graphics"], &meta)
	meta.DriveEnabled = flag
	doc["x-puppeteer-graphics"], _ = json.Marshal(meta)
	doc["services"], _ = json.Marshal(m.Services)
	out, err := json.Marshal(doc)
	return ComposePlan{Document: out}, err
}
func driveBinary() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", errors.New("共享盘执行程序不可读取")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return "", errors.New("共享盘执行程序必须不可被非所有者改写")
	}
	return path, nil
}
func (d *HostDriver) drivePlan(ctx context.Context, raw []byte, r Request) (ComposePlan, error) {
	helper, err := driveBinary()
	if err != nil {
		return ComposePlan{}, err
	}
	profile, err := d.driveSecurity(ctx, r.Enabled, false)
	if err != nil {
		return ComposePlan{}, err
	}
	return planDrive(raw, r, helper, profile)
}
func (d *HostDriver) applyDrive(ctx context.Context, raw []byte, r Request) error {
	// Stop old FUSE workers before replacing admission, including failed disables.
	if err := d.Executor.stopGraphics(ctx); err != nil {
		return err
	}
	if r.Enabled {
		if _, err := d.driveSecurity(ctx, true, true); err != nil {
			return err
		}
	}
	return d.Executor.applyGraphics(ctx, graphicsEnabled(raw))
}
func (e ComposeExecutor) verifyDrive(ctx context.Context, enabled bool) error {
	raw, err := e.Resolve(ctx)
	if err != nil {
		return err
	}
	if err = e.verifyGraphics(ctx, graphicsEnabled(raw)); err != nil {
		return err
	}
	if !graphicsEnabled(raw) {
		if enabled {
			return errors.New("共享盘依赖图形组件运行")
		}
		return nil
	}
	if enabled {
		_, err = e.run(ctx, 20*time.Second, "verify mounted storage I/O", "exec", "-T", "guacd", driveHelperTarget, "rdp-storage", "verify")
		return err
	}
	_, err = e.run(ctx, 5*time.Second, "verify storage unmounted", "exec", "-T", "guacd", "sh", "-c", `! grep -q ' /mnt/nt-rdp ' /proc/self/mountinfo && test ! -e /dev/fuse && test ! -e /usr/local/bin/puppeteer-drive && test ! -e /var/lib/puppeteer/storage/active.json`)
	return err
}

func (d *HostDriver) withDriveComponent(ctx context.Context, raw []byte, graphics ComponentStatus) map[string]ComponentStatus {
	drive := ComponentStatus{State: "unknown", ObservedAt: time.Now().UTC(), Enabled: driveDesired(raw), Reason: "共享盘配方未接管"}
	helper, _ := driveBinary()
	_, err := planDrive(raw, Request{Protocol: "rdp-drive", Enabled: false}, helper, driveProfileName(d.Executor.Project))
	if err != nil {
		drive.Reason = err.Error()
	} else {
		drive.Supported = true
		drive.Reason = "共享盘挂载状态不可读取"
		if graphics.State == "stopped" {
			drive.State = "stopped"
			drive.Reason = "图形容器已停止，共享盘未挂载"
		} else if graphics.State == "running" {
			mounts, e := d.Executor.run(ctx, time.Second, "observe storage mount", "exec", "-T", "guacd", "cat", "/proc/self/mountinfo")
			if e == nil {
				drive.State = "stopped"
				drive.Reason = "共享盘未挂载；网页 MyZone 数据保留"
				if strings.Contains(string(mounts), " /mnt/nt-rdp ") {
					drive.State = "running"
					drive.Reason = "共享盘已挂载；不代表用户或目标资产具备文件权限"
				}
			}
		}
	}
	return map[string]ComponentStatus{"graphics": graphics, "rdp-drive": drive}
}
