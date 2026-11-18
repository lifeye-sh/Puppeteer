package deploytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"puppeteer/backend/internal/platform/resourcebudget"
	"strconv"
	"strings"
	"time"
)

const scratchTarget = "/var/lib/puppeteer-renderer"

func validateResourceRequest(r Request) error {
	if !r.Enabled || r.Bind != "" || r.Port != 0 || r.SiteID != "" || r.SiteVersion != 0 || r.SiteEnabled || r.AllowCIDRs != "" || r.AdminIP != "" || r.Repository != "" || r.TrustRoot != "" {
		return errors.New("资源档位操作仅接受档位名称")
	}
	_, err := resourcebudget.Lookup(r.ResourceProfile)
	return err
}

// Scratch has one dedicated project-owned named volume. Arbitrary host paths,
// external volumes and driver options never enter the privileged task contract.
func validScratch(doc map[string]json.RawMessage, c map[string]json.RawMessage, env map[string]string) bool {
	var mounts []struct {
		Type, Source, Target string
		ReadOnly             bool `json:"read_only"`
		Volume               struct {
			Subpath string
			NoCopy  bool `json:"nocopy"`
		}
	}
	if json.Unmarshal(c["volumes"], &mounts) != nil || len(mounts) != 1 {
		return false
	}
	v := mounts[0]
	if v.Type != "volume" || v.Source != "mp4scratch" || v.Target != scratchTarget || v.ReadOnly || v.Volume.Subpath != "" || v.Volume.NoCopy {
		return false
	}
	var project string
	var volumes map[string]map[string]json.RawMessage
	if json.Unmarshal(doc["name"], &project) != nil || !projectName.MatchString(project) || json.Unmarshal(doc["volumes"], &volumes) != nil {
		return false
	}
	volume, ok := volumes["mp4scratch"]
	if !ok {
		return false
	}
	for key, raw := range volume {
		switch key {
		case "name":
			var name string
			if json.Unmarshal(raw, &name) != nil || name != project+"_mp4scratch" {
				return false
			}
		case "driver":
			if string(raw) != `"local"` {
				return false
			}
		case "labels": // Compose owns these inert labels.
		default:
			return false
		}
	}
	quota, err := strconv.Atoi(env["NT_MP4_SCRATCH_QUOTA_MIB"])
	return err == nil && quota >= 768 && quota <= 65536 && env["NT_MP4_SCRATCH_DIRECTORY"] == scratchTarget
}

func planResources(raw []byte, r Request) (ComposePlan, error) {
	if err := Validate(r); err != nil {
		return ComposePlan{}, err
	}
	p, _ := resourcebudget.Lookup(r.ResourceProfile)
	var doc map[string]json.RawMessage
	var m composeModel
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &m) != nil {
		return ComposePlan{}, errors.New("部署配置不可解析")
	}
	for _, name := range []string{"server", "web", "postgres"} {
		if m.Services[name] == nil {
			return ComposePlan{}, errors.New("资源档位需要标准 server/web/postgres 单机部署")
		}
	}
	for _, svc := range m.Services {
		if svc["deploy"] != nil || svc["scale"] != nil {
			return ComposePlan{}, errors.New("自定义副本或 deploy 资源约束需要先合并为单机部署")
		}
	}
	var env map[string]string
	if json.Unmarshal(m.Services["server"]["environment"], &env) != nil {
		return ComposePlan{}, errors.New("应用环境配置不可读取")
	}
	if m.Services["guacd"] != nil {
		if _, err := planGraphics(raw, Request{Protocol: "graphics", Enabled: graphicsEnabled(raw)}); err != nil {
			return ComposePlan{}, err
		}
	}
	if c := m.Services["mp4-renderer"]; c != nil {
		if _, err := planMP4(raw, Request{Protocol: "mp4", Enabled: mp4Enabled(raw)}); err != nil {
			return ComposePlan{}, err
		}
		var project string
		if json.Unmarshal(doc["name"], &project) != nil || !projectName.MatchString(project) {
			return ComposePlan{}, errors.New("缺少标准 Compose 项目名称")
		}
		var ce map[string]string
		_ = json.Unmarshal(c["environment"], &ce)
		var volumes map[string]map[string]json.RawMessage
		if doc["volumes"] != nil && json.Unmarshal(doc["volumes"], &volumes) != nil {
			return ComposePlan{}, errors.New("卷配置不可读取")
		}
		if volumes == nil {
			volumes = map[string]map[string]json.RawMessage{}
		}
		if _, exists := volumes["mp4scratch"]; exists && !validScratch(doc, c, ce) {
			return ComposePlan{}, errors.New("mp4scratch 已被其他用途占用")
		}
		// No other service may read or modify renderer working files.
		for name, svc := range m.Services {
			if name != "mp4-renderer" {
				var mounts []struct{ Source string }
				_ = json.Unmarshal(svc["volumes"], &mounts)
				for _, v := range mounts {
					if v.Source == "mp4scratch" {
						return ComposePlan{}, errors.New("MP4 临时卷被其他服务引用")
					}
				}
			}
		}
		volumes["mp4scratch"] = map[string]json.RawMessage{"name": jsonValue(project + "_mp4scratch")}
		doc["volumes"] = jsonValue(volumes)
		ce["NT_MP4_SCRATCH_DIRECTORY"] = scratchTarget
		ce["NT_MP4_SCRATCH_QUOTA_MIB"] = strconv.Itoa(p.ScratchMiB)
		c["environment"] = jsonValue(ce)
		c["volumes"] = jsonValue([]map[string]string{{"type": "volume", "source": "mp4scratch", "target": scratchTarget}})
		c["tmpfs"] = jsonValue([]string{"/tmp:rw,size=128m,mode=1777"})
	}
	if err := applyDatabaseBudget(doc, m, p.Name); err != nil {
		return ComposePlan{}, err
	}
	env["NT_RESOURCE_PROFILE"] = p.Name
	env["GOMEMLIMIT"] = fmt.Sprintf("%dMiB", p.GoMiB)
	m.Services["server"]["environment"] = jsonValue(env)
	budgets := map[string]int{"server": p.ServerMiB, "postgres": p.DatabaseMiB, "guacd": p.GraphicsMiB, "mp4-renderer": p.RendererMiB, "web": p.WebMiB}
	for name, mib := range budgets {
		if s := m.Services[name]; s != nil {
			s["mem_limit"] = jsonValue(int64(mib) << 20)
			s["memswap_limit"] = jsonValue(int64(mib) << 20)
			delete(s, "mem_reservation")
			s["cpus"] = jsonValue(float64(p.CPUs) / 2)
		}
	}
	doc["services"] = jsonValue(m.Services)
	out, err := json.Marshal(doc)
	if err == nil && m.Services["mp4-renderer"] != nil {
		_, err = planMP4(out, Request{Protocol: "mp4", Enabled: mp4Enabled(out)})
	}
	return ComposePlan{Document: out}, err
}

func jsonValue(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }

func (d *HostDriver) withResourceComponent(raw []byte, result map[string]ComponentStatus) map[string]ComponentStatus {
	s := ComponentStatus{State: "unknown", ObservedAt: time.Now().UTC(), Reason: "资源档位需要已接管的标准单机部署"}
	if len(raw) > 0 {
		var m composeModel
		var env map[string]string
		_ = json.Unmarshal(raw, &m)
		_ = json.Unmarshal(m.Services["server"]["environment"], &env)
		s.Profile = env["NT_RESOURCE_PROFILE"]
		_, err := planResources(raw, Request{Protocol: "resources", Enabled: true, ResourceProfile: "standard"})
		if err == nil {
			s.Supported = true
			s.Enabled = true
			s.Reason = "配置可管理；实际镜像能力、主机容量及应用状态将在提交前检查"
		} else {
			s.Reason = err.Error()
		}
	}
	result["resources"] = s
	return result
}

func (e ComposeExecutor) resourcePreflight(ctx context.Context, raw []byte, r Request) error {
	p, _ := resourcebudget.Lookup(r.ResourceProfile)
	info, err := e.execute(ctx, 10*time.Second, "read Docker capacity", "info", "--format", "{{json .}}")
	var host struct {
		MemTotal      int64
		NCPU          int
		DockerRootDir string
	}
	// Use the same Docker-visible floor as installation for the light profile.
	if err != nil || json.Unmarshal(info, &host) != nil {
		return errors.New("无法读取 Docker 主机资源")
	}
	if host.MemTotal < int64(p.MinimumHostMiB())<<20 || host.NCPU < p.CPUs {
		return fmt.Errorf("%s 档位预检需要至少 %d 核及 Docker 可见内存 %d MiB；当前 %d 核、%d MiB", p.Name, p.CPUs, p.MinimumHostMiB(), host.NCPU, host.MemTotal>>20)
	}
	var m composeModel
	_ = json.Unmarshal(raw, &m)
	var serverImage string
	_ = json.Unmarshal(m.Services["server"]["image"], &serverImage)
	serverLabel, labelErr := e.execute(ctx, 10*time.Second, "check resource application image", "image", "inspect", serverImage, "--format", `{{index .Config.Labels "io.puppeteer.resources"}}`)
	if labelErr != nil || strings.TrimSpace(string(serverLabel)) != "v1" {
		return errors.New("应用镜像不支持资源档位，请先升级匹配的服务端镜像")
	}
	dbLabel, dbErr := e.execute(ctx, 10*time.Second, "check database budget image", "image", "inspect", serverImage, "--format", `{{index .Config.Labels "io.puppeteer.database.budget"}}`)
	if dbErr != nil || strings.TrimSpace(string(dbLabel)) != "v1" {
		return errors.New("应用镜像不支持数据库预算，请升级同批镜像")
	}
	if c := m.Services["mp4-renderer"]; c != nil {
		if err := checkResourceDisk(host.DockerRootDir); err != nil {
			return err
		}
		if err := e.checkScratchOwnership(ctx); err != nil {
			return err
		}
		var image string
		_ = json.Unmarshal(c["image"], &image)
		label, err := e.execute(ctx, 10*time.Second, "check streaming renderer image", "image", "inspect", image, "--format", `{{index .Config.Labels "io.puppeteer.renderer.streaming"}}`)
		if err != nil || strings.TrimSpace(string(label)) != "v1" {
			return errors.New("渲染器镜像不支持流式转换及磁盘临时目录，请先升级匹配的镜像")
		}
	}
	return nil
}

func (e ComposeExecutor) checkScratchOwnership(ctx context.Context) error {
	name := e.Project + "_mp4scratch"
	out, err := e.execute(ctx, 10*time.Second, "find renderer scratch volume", "volume", "ls", "--filter", "name=^"+name+"$", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	if strings.TrimSpace(string(out)) != name {
		return errors.New("MP4 临时卷名称存在冲突")
	}
	out, err = e.execute(ctx, 10*time.Second, "verify renderer scratch ownership", "volume", "inspect", name, "--format", "{{json .}}")
	var v struct {
		Driver  string
		Options map[string]string
		Labels  map[string]string
	}
	if err != nil || json.Unmarshal(out, &v) != nil || v.Driver != "local" || len(v.Options) != 0 || v.Labels["com.docker.compose.project"] != e.Project || v.Labels["com.docker.compose.volume"] != "mp4scratch" {
		return errors.New("同名 MP4 临时卷不属于当前项目或使用自定义驱动，拒绝接管")
	}
	return nil
}

// Only enrolled services are recreated, with the old document journaled first.
// Disabled components keep their profiles; volumes, credentials and ports stay.
func (e ComposeExecutor) applyResources(ctx context.Context, progress ...func(string) error) error {
	report := func(stage string) error {
		if len(progress) > 0 && progress[0] != nil {
			return progress[0](stage)
		}
		return nil
	}
	if err := e.Validate(ctx); err != nil {
		return err
	}
	raw, err := e.Resolve(ctx)
	if err != nil {
		return err
	}
	var m composeModel
	_ = json.Unmarshal(raw, &m)
	if err = report("resource-stop"); err != nil {
		return err
	}
	for _, name := range []string{"server", "guacd", "mp4-renderer"} {
		if m.Services[name] != nil {
			if _, err = e.run(ctx, 60*time.Second, "pause resource consumers", "stop", "--timeout", "30", name); err != nil {
				return err
			}
		}
	}
	names := []string{"postgres"}
	if mp4Enabled(raw) {
		names = append(names, "mp4-renderer")
	}
	// Graphics depends on the application, so start it after server.
	names = append(names, "server", "web")
	if graphicsEnabled(raw) {
		names = append(names, "guacd")
	}
	var rendererEnv map[string]string
	_ = json.Unmarshal(m.Services["mp4-renderer"]["environment"], &rendererEnv)
	for _, name := range names {
		if err = report("resource-" + name); err != nil {
			return err
		}
		if _, err = e.run(ctx, 3*time.Minute, "apply resource budget to "+name, "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", name); err != nil {
			return err
		}
		if name == "mp4-renderer" && rendererEnv["NT_MP4_SCRATCH_DIRECTORY"] != "" {
			if _, err = e.run(ctx, 15*time.Second, "check renderer scratch admission", "exec", "-T", "mp4-renderer", "node", "--input-type=module", "-e", "const s=await import('./scratch.mjs');await s.admitScratch();"); err != nil {
				return errors.New("MP4 临时卷权限、可用空间或配额检查失败")
			}
		}
	}
	return nil
}

func (e ComposeExecutor) verifyResources(ctx context.Context, r Request) error {
	raw, err := e.Resolve(ctx)
	if err != nil {
		return err
	}
	plan, err := planResources(raw, r)
	if err != nil {
		return err
	}
	var expected composeModel
	_ = json.Unmarshal(plan.Document, &expected)
	for _, name := range []string{"postgres", "server", "web", "guacd", "mp4-renderer"} {
		if expected.Services[name] == nil || name == "guacd" && !graphicsEnabled(raw) || name == "mp4-renderer" && !mp4Enabled(raw) {
			continue
		}
		ids, err := e.run(ctx, 10*time.Second, "inspect resource service", "ps", "-q", name)
		if err != nil || len(strings.Fields(string(ids))) != 1 {
			return errors.New("资源部署服务实例不符合单机约束")
		}
		out, err := e.execute(ctx, 10*time.Second, "verify resource limits", "inspect", strings.TrimSpace(string(ids)), "--format", "{{json .}}")
		var container struct {
			HostConfig struct{ Memory, MemorySwap, NanoCpus int64 }
			State      struct {
				Running bool
				Health  struct{ Status string }
			}
		}
		if err != nil || json.Unmarshal(out, &container) != nil || !container.State.Running || container.State.Health.Status != "healthy" {
			return errors.New("资源部署服务健康检查失败")
		}
		var memory int64
		var cpus float64
		_ = json.Unmarshal(expected.Services[name]["mem_limit"], &memory)
		_ = json.Unmarshal(expected.Services[name]["cpus"], &cpus)
		if container.HostConfig.Memory != memory || container.HostConfig.MemorySwap != memory || container.HostConfig.NanoCpus != int64(cpus*1e9) {
			return errors.New("实际容器资源限制与所选档位不一致")
		}
	}
	budget, _ := resourcebudget.Database(r.ResourceProfile)
	query := `SELECT json_build_object('max_connections',current_setting('max_connections'),'shared_buffers',current_setting('shared_buffers'),'work_mem',current_setting('work_mem'),'maintenance_work_mem',current_setting('maintenance_work_mem'))`
	data, err := e.run(ctx, 10*time.Second, "verify database resource parameters", "exec", "-T", "postgres", "sh", "-c", `exec psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"`, "sh", query)
	var actual map[string]string
	if err != nil || json.Unmarshal(data, &actual) != nil {
		return errors.New("无法验证数据库实际资源参数")
	}
	for key, value := range budget.Parameters() {
		if actual[key] != value {
			return errors.New("数据库实际参数与所选档位不一致：" + key)
		}
	}

	return nil
}
