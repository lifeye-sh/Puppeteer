package deploytask

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed rdp-storage.apparmor
var driveProfileTemplate string

func driveProfileName(project string) string { return "puppeteer-rdp-storage-" + project }
func (d *HostDriver) driveSecurity(ctx context.Context, enabled, apply bool) (string, error) {
	if enabled {
		info, err := os.Stat("/dev/fuse")
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return "", errors.New("宿主 /dev/fuse 不可用，当前环境不能部署 RDP 共享盘")
		}
	}
	raw, err := d.Executor.execute(ctx, 5*time.Second, "inspect storage host security", "info", "--format", "{{json .SecurityOptions}}")
	if err != nil {
		return "", err
	}
	var options []string
	if json.Unmarshal(raw, &options) != nil {
		return "", errors.New("无法识别 Docker 安全能力")
	}
	apparmor := false
	for _, opt := range options {
		if strings.Contains(opt, "apparmor") {
			apparmor = true
		}
		if enabled && (strings.Contains(opt, "rootless") || strings.Contains(opt, "selinux")) {
			return "", errors.New("共享盘自动部署暂不支持 rootless 或 SELinux 宿主")
		}
	}
	if !apparmor {
		return "", nil
	}
	name := driveProfileName(d.Executor.Project)
	if !enabled {
		return name, nil
	}
	parser, err := exec.LookPath("apparmor_parser")
	if err != nil {
		return "", errors.New("宿主启用了 AppArmor，但缺少 apparmor_parser，无法加载限定挂载策略")
	}
	content := []byte(strings.ReplaceAll(driveProfileTemplate, "puppeteer-rdp-storage", name))
	destination := filepath.Join("/etc/apparmor.d", name)
	if info, statErr := os.Lstat(destination); statErr == nil {
		old, readErr := os.ReadFile(destination)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || readErr != nil || !bytes.Equal(old, content) {
			return "", errors.New("同名 AppArmor 策略已有自定义内容，拒绝覆盖")
		}
	} else if !os.IsNotExist(statErr) {
		return "", errors.New("无法检查 AppArmor 策略目录")
	}
	f, err := os.CreateTemp(d.StateDirectory, ".drive-policy-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	run := func(args ...string) error {
		child, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(child, parser, args...)
		cmd.WaitDelay = time.Second
		cmd.Stdout = nil
		cmd.Stderr = nil
		if cmd.Run() != nil {
			return errors.New("AppArmor 限定挂载策略校验或加载失败")
		}
		return nil
	}
	if err = run("--skip-kernel-load", "--skip-cache", f.Name()); err != nil {
		return "", err
	}
	if !apply {
		return name, nil
	}
	// Persist only the fixed, namespaced profile; never disable AppArmor or replace another policy.
	if err = os.MkdirAll("/etc/apparmor.d", 0755); err != nil {
		return "", err
	}
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		_, err = target.Write(content)
		if err == nil {
			err = target.Sync()
		}
		closeErr := target.Close()
		if err != nil || closeErr != nil {
			return "", errors.New("AppArmor 策略保存失败")
		}
	} else if !os.IsExist(err) {
		return "", errors.New("AppArmor 策略不可保存")
	}
	if err = run("-r", destination); err != nil {
		return "", err
	}
	return name, nil
}
