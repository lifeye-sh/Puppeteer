package deploytask

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const mp4Profile = "puppeteer-mp4-disabled"

func mp4Enabled(raw []byte) bool {
	var m composeModel
	var env map[string]string
	return json.Unmarshal(raw, &m) == nil && json.Unmarshal(m.Services["server"]["environment"], &env) == nil && env["NT_MP4_URL"] != ""
}

// The installer owns the recipe. A request can change only admission and the
// component profile; it cannot provide an image, secret, mount or public port.
func planMP4(raw []byte, r Request) (ComposePlan, error) {
	if err := Validate(r); err != nil {
		return ComposePlan{}, err
	}
	var doc map[string]json.RawMessage
	var m composeModel
	var enrollment struct{ Version int }
	fail := func() (ComposePlan, error) {
		return ComposePlan{}, errors.New("MP4 组件尚未接管或部署配方不兼容；请使用匹配的安装升级脚本")
	}
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &m) != nil || json.Unmarshal(doc["x-puppeteer-mp4"], &enrollment) != nil || enrollment.Version != 1 {
		return fail()
	}
	s, c := m.Services["server"], m.Services["mp4-renderer"]
	var env, ce, labels map[string]string
	var profiles []string
	var image string
	if s == nil || c == nil || json.Unmarshal(s["environment"], &env) != nil || json.Unmarshal(c["environment"], &ce) != nil || json.Unmarshal(c["labels"], &labels) != nil || labels["io.puppeteer.managed.mp4"] != "v1" {
		return fail()
	}
	if env["NT_MP4_URL"] != "" && env["NT_MP4_URL"] != "http://mp4-renderer:8090" || env["NT_MP4_TOKEN_FILE"] != "/run/secrets/mp4_token" || ce["NT_MP4_TOKEN_FILE"] != env["NT_MP4_TOKEN_FILE"] || env["NT_MP4_TOKEN"] != "" || ce["NT_MP4_TOKEN"] != "" || env["NT_MP4_DIRECTORY"] != "/var/lib/puppeteer/mp4" {
		return ComposePlan{}, errors.New("MP4 地址、通信密钥文件或缓存目录不符合内部部署约定")
	}
	var mounts []struct {
		Type, Source, Target string
		ReadOnly             bool `json:"read_only"`
	}
	if json.Unmarshal(s["volumes"], &mounts) != nil {
		return ComposePlan{}, errors.New("MP4 缓存缺少持久化卷")
	}
	persistent, longest := false, 0
	for _, mount := range mounts {
		if env["NT_MP4_DIRECTORY"] == mount.Target || strings.HasPrefix(env["NT_MP4_DIRECTORY"], strings.TrimRight(mount.Target, "/")+"/") {
			if len(mount.Target) > longest {
				longest = len(mount.Target)
				persistent = (mount.Type == "volume" || mount.Type == "bind") && mount.Source != "" && !mount.ReadOnly
			}
		}
	}
	var temporary []string
	if s["tmpfs"] != nil && json.Unmarshal(s["tmpfs"], &temporary) != nil {
		return ComposePlan{}, errors.New("MP4 缓存临时挂载配置无效")
	}
	for _, mount := range temporary {
		target := strings.SplitN(mount, ":", 2)[0]
		if env["NT_MP4_DIRECTORY"] == target || strings.HasPrefix(env["NT_MP4_DIRECTORY"], strings.TrimRight(target, "/")+"/") {
			persistent = false
		}
	}
	if !persistent {
		return ComposePlan{}, errors.New("MP4 缓存必须位于可写持久化卷，不能使用临时或只读挂载")
	}
	if json.Unmarshal(c["image"], &image) != nil || strings.HasSuffix(image, ":latest") || strings.LastIndex(image, ":") <= strings.LastIndex(image, "/") {
		return ComposePlan{}, errors.New("MP4 渲染器必须使用明确版本或摘要的镜像")
	}
	if c["profiles"] != nil && (json.Unmarshal(c["profiles"], &profiles) != nil || len(profiles) > 1 || len(profiles) == 1 && profiles[0] != mp4Profile) {
		return ComposePlan{}, errors.New("MP4 自定义 profiles 不支持自动部署")
	}
	var networks map[string]struct{ Internal, External bool }
	if json.Unmarshal(doc["networks"], &networks) != nil || !networks["render"].Internal || networks["render"].External {
		return ComposePlan{}, errors.New("MP4 render 网络必须是本项目内部网络")
	}
	for index, svc := range []map[string]json.RawMessage{s, c} {
		var nets map[string]json.RawMessage
		if svc["network_mode"] != nil || json.Unmarshal(svc["networks"], &nets) != nil {
			return ComposePlan{}, errors.New("MP4 需要显式网络配置，不支持 network_mode")
		}
		if _, ok := nets["render"]; !ok {
			return ComposePlan{}, errors.New("应用和渲染器必须连接 render 网络")
		}
		if index == 1 && len(nets) != 1 {
			return ComposePlan{}, errors.New("MP4 渲染器只能连接内部 render 网络")
		}
		var secrets []struct{ Source, Target string }
		if json.Unmarshal(svc["secrets"], &secrets) != nil {
			return ComposePlan{}, errors.New("MP4 通信密钥挂载定义不可解析")
		}
		matches := 0
		for _, secret := range secrets {
			if secret.Target == "mp4_token" || secret.Target == "/run/secrets/mp4_token" || secret.Target == "" && secret.Source == "mp4_token" {
				if secret.Source != "mp4_token" {
					return ComposePlan{}, errors.New("应用和渲染器必须使用同一 mp4_token 密钥来源")
				}
				matches++
			}
		}
		if matches != 1 {
			return ComposePlan{}, errors.New("MP4 通信密钥必须唯一挂载到 /run/secrets/mp4_token")
		}
	}
	var secrets map[string]struct {
		File     string
		External bool
	}
	if json.Unmarshal(doc["secrets"], &secrets) != nil || secrets["mp4_token"].File == "" || secrets["mp4_token"].External {
		return ComposePlan{}, errors.New("MP4 通信密钥必须来自本地文件 secret")
	}
	for _, key := range []string{"ports", "devices", "cap_add"} {
		var values []json.RawMessage
		if c[key] != nil && (json.Unmarshal(c[key], &values) != nil || len(values) > 0) {
			return ComposePlan{}, errors.New("MP4 渲染器不支持宿主端口、设备、额外挂载或附加 capabilities")
		}
	}
	diskScratch := ce["NT_MP4_SCRATCH_DIRECTORY"] != "" || ce["NT_MP4_SCRATCH_QUOTA_MIB"] != ""
	if diskScratch {
		if !validScratch(doc, c, ce) {
			return ComposePlan{}, errors.New("MP4 磁盘临时目录必须使用本项目独占的 mp4scratch 卷及有效配额")
		}
	} else {
		var volumes []json.RawMessage
		if c["volumes"] != nil && (json.Unmarshal(c["volumes"], &volumes) != nil || len(volumes) > 0) {
			return ComposePlan{}, errors.New("MP4 不接受非受管的临时卷")
		}
	}
	for _, key := range []string{"privileged", "pid", "ipc", "command", "entrypoint"} {
		if c[key] != nil && string(c[key]) != "null" && string(c[key]) != "false" {
			return ComposePlan{}, errors.New("MP4 渲染器不支持特权、共享命名空间或覆盖启动命令")
		}
	}
	var ro bool
	var caps, security, tmpfs []string
	var memory int64
	if json.Unmarshal(c["mem_limit"], &memory) != nil {
		var bytes string
		if json.Unmarshal(c["mem_limit"], &bytes) == nil {
			memory, _ = strconv.ParseInt(bytes, 10, 64)
		}
	}
	var cpus float64
	var pids int
	var health struct {
		Test    []string
		Disable bool
	}
	expectedTmpfs := "/tmp:rw,size=768m,mode=1777"
	if diskScratch {
		expectedTmpfs = "/tmp:rw,size=128m,mode=1777"
	}
	if json.Unmarshal(c["read_only"], &ro) != nil || !ro || json.Unmarshal(c["cap_drop"], &caps) != nil || len(caps) != 1 || strings.ToUpper(caps[0]) != "ALL" || json.Unmarshal(c["security_opt"], &security) != nil || len(security) != 1 || security[0] != "no-new-privileges:true" && security[0] != "no-new-privileges" || json.Unmarshal(c["tmpfs"], &tmpfs) != nil || len(tmpfs) != 1 || tmpfs[0] != expectedTmpfs || memory < 512<<20 || memory > 4<<30 || json.Unmarshal(c["cpus"], &cpus) != nil || cpus <= 0 || cpus > 4 || json.Unmarshal(c["pids_limit"], &pids) != nil || pids < 64 || pids > 512 || json.Unmarshal(c["healthcheck"], &health) != nil || health.Disable || len(health.Test) < 2 || health.Test[0] == "NONE" {
		return ComposePlan{}, errors.New("MP4 资源与隔离要求不满足：只读文件系统、ALL 权限移除、no-new-privileges、768 MiB 临时目录、512 MiB–4 GiB 内存、至多 4 CPU、64–512 PID 及健康检查")
	}
	for name, svc := range m.Services {
		var deps map[string]json.RawMessage
		if svc["depends_on"] != nil {
			if json.Unmarshal(svc["depends_on"], &deps) != nil {
				return fail()
			}
			if deps["mp4-renderer"] != nil {
				if name != "server" {
					return ComposePlan{}, errors.New("存在依赖 MP4 渲染器的自定义服务，无法独立启停")
				}
				delete(deps, "mp4-renderer")
				svc["depends_on"], _ = json.Marshal(deps)
			}
		}
	}
	if r.Enabled {
		delete(c, "profiles")
		env["NT_MP4_URL"] = "http://mp4-renderer:8090"
	} else {
		c["profiles"], _ = json.Marshal([]string{mp4Profile})
		env["NT_MP4_URL"] = ""
	}
	env["NT_DEPLOY_MANAGED_MP4"] = map[bool]string{true: "true", false: "false"}[r.Enabled]
	if r.RendererMode != "" {
		env["NT_MP4_ON_DEMAND"] = strconv.FormatBool(r.RendererMode == "on-demand")
		c["restart"], _ = json.Marshal(map[bool]string{true: "no", false: "unless-stopped"}[r.RendererMode == "on-demand"])
	}
	if r.RendererQuality != "" {
		ce["NT_MP4_QUALITY"] = r.RendererQuality
	}
	if env["NT_MP4_ON_DEMAND"] != "" && env["NT_MP4_ON_DEMAND"] != "true" && env["NT_MP4_ON_DEMAND"] != "false" {
		return ComposePlan{}, errors.New("MP4 按需运行配置无效")
	}
	var restart string
	if env["NT_MP4_ON_DEMAND"] == "true" && (json.Unmarshal(c["restart"], &restart) != nil || restart != "no") {
		return ComposePlan{}, errors.New("按需渲染器必须关闭 Docker 自动重启策略，请重新应用运行方式")
	}
	if ce["NT_MP4_QUALITY"] != "" && ce["NT_MP4_QUALITY"] != "economy" && ce["NT_MP4_QUALITY"] != "balanced" && ce["NT_MP4_QUALITY"] != "sharp" {
		return ComposePlan{}, errors.New("渲染器质量配置无效")
	}
	c["environment"], _ = json.Marshal(ce)
	s["environment"], _ = json.Marshal(env)
	doc["services"], _ = json.Marshal(m.Services)
	out, err := json.Marshal(doc)
	return ComposePlan{Document: out}, err
}

func (e ComposeExecutor) stopMP4(ctx context.Context) error {
	// Stop admission and the sole worker before reconciling its leases. Always try
	// stopping the renderer too, including when the backend stop failed.
	_, serverErr := e.run(ctx, 45*time.Second, "pause MP4 worker", "stop", "--timeout", "30", "server")
	_, rendererErr := e.run(ctx, 45*time.Second, "stop MP4 renderer", "stop", "--timeout", "15", "mp4-renderer")
	return errors.Join(serverErr, rendererErr)
}

func (e ComposeExecutor) applyMP4(ctx context.Context, enabled bool) error {
	if err := e.Validate(ctx); err != nil {
		return err
	}
	if enabled {
		if _, err := e.run(ctx, 4*time.Minute, "pull MP4 renderer", "pull", "--policy", "missing", "mp4-renderer"); err != nil {
			return err
		}
	}
	if err := e.stopMP4(ctx); err != nil {
		return err
	}
	raw, err := e.run(ctx, 10*time.Second, "verify paused MP4 worker", "ps", "--all", "--format", "json", "server")
	if err != nil {
		return err
	}
	containers, err := parseGraphicsContainers(raw)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.Service != "server" || c.State != "exited" && c.State != "created" {
			return errors.New("MP4 工作进程未停止，拒绝重置队列")
		}
	}
	if _, err = e.run(ctx, time.Minute, "preserve MP4 queue", "run", "--rm", "--no-deps", "--pull", "never", "-T", "--entrypoint", "/app/nt", "server", "deployment-mp4-queue"); err != nil {
		return err
	}
	if enabled {
		if _, err = e.run(ctx, 3*time.Minute, "start MP4 renderer", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "mp4-renderer"); err != nil {
			return err
		}
		if _, err = e.run(ctx, 90*time.Second, "verify MP4 sample conversion", "run", "--rm", "--no-deps", "--pull", "never", "-T", "--entrypoint", "/app/nt", "server", "deployment-mp4-sample"); err != nil {
			return err
		}
	}
	return e.Apply(ctx)
}

func (e ComposeExecutor) verifyMP4(ctx context.Context, enabled bool) error {
	raw, err := e.run(ctx, 15*time.Second, "verify MP4 containers", "ps", "--all", "--format", "json", "server", "web", "mp4-renderer")
	if err != nil {
		return err
	}
	list, err := parseGraphicsContainers(raw)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, c := range list {
		counts[c.Service]++
		if c.Service == "server" || c.Service == "web" || c.Service == "mp4-renderer" && enabled {
			if c.State != "running" || c.Health != "healthy" {
				return errors.New("应用或 MP4 组件未通过健康检查")
			}
		}
		if c.Service == "mp4-renderer" {
			if !enabled && c.State != "exited" && c.State != "created" {
				return errors.New("MP4 组件仍在运行")
			}
			for _, p := range c.Publishers {
				if p.PublishedPort != 0 {
					return errors.New("MP4 组件不得映射宿主端口")
				}
			}
		}
	}
	if counts["server"] != 1 || counts["web"] != 1 || enabled && counts["mp4-renderer"] != 1 || counts["mp4-renderer"] > 1 {
		return errors.New("MP4 组件实例数量不符合单机部署约束")
	}
	return nil
}

func (d *HostDriver) withMP4Component(ctx context.Context, raw []byte, result map[string]ComponentStatus) map[string]ComponentStatus {
	c := ComponentStatus{Enabled: mp4Enabled(raw), State: "unknown", ObservedAt: time.Now().UTC()}
	c.Mode, c.Quality = rendererSettings(raw)
	if _, err := planMP4(raw, Request{Protocol: "mp4", Enabled: c.Enabled}); err != nil {
		c.Reason = err.Error()
	} else {
		c.Supported = true
		c.Reason = "MP4 容器状态无法读取"
		if b, err := d.Executor.run(ctx, 2*time.Second, "observe MP4", "ps", "--all", "--format", "json", "mp4-renderer"); err == nil {
			if list, err := parseGraphicsContainers(b); err == nil {
				c.State, c.Reason = "stopped", "MP4 渲染器已停止或尚未创建"
				if len(list) > 1 {
					c.State, c.Reason = "failed", "MP4 组件实例数量异常"
				}
				if len(list) == 1 {
					s := list[0]
					if s.State != "exited" && s.State != "created" {
						c.State, c.Reason = "failed", "MP4 渲染器未通过健康检查"
						if s.State == "running" && s.Health == "healthy" {
							c.State, c.Reason = "running", "MP4 渲染器容器健康"
						}
					}
					if s.State == "exited" && s.ExitCode != 0 {
						c.State, c.Reason = "failed", "MP4 渲染器异常退出；请检查容器日志与资源限制"
					}
					for _, p := range s.Publishers {
						if p.PublishedPort != 0 {
							c.State, c.Reason = "failed", "MP4 组件存在宿主端口"
						}
					}
				}
			}
		}
	}
	if c.Enabled && c.Mode == "on-demand" && c.State == "stopped" {
		c.State = "standby"
		c.Reason = "按需待机；收到转换任务后只唤醒渲染器，应用与网页保持运行"
	}
	if c.Enabled && c.Mode == "on-demand" {
		if message := d.rendererMaintenanceError.Load(); message != nil {
			c.Reason += "；自动休眠未完成，将重试：" + *message
		}
	}
	result["mp4"] = c
	return result
}
