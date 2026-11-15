package deploytask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

const graphicsProfile = "puppeteer-graphics-disabled"
const graphicsOwner = "io.puppeteer.managed.graphics"

type graphicsEnrollment struct {
	Version      int    `json:"version"`
	DriveEnabled string `json:"driveEnabled"`
}

// Graphics uses an installer-enrolled recipe, never a caller-provided image,
// mount, command or port. The recipe remains present while stopped so upgrades
// and crash recovery preserve its image, FUSE configuration and volume identity.
func planGraphics(raw []byte, r Request) (ComposePlan, error) {
	if err := Validate(r); err != nil {
		return ComposePlan{}, err
	}
	var doc map[string]json.RawMessage
	var model composeModel
	var enrollment graphicsEnrollment
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &model) != nil || json.Unmarshal(doc["x-puppeteer-graphics"], &enrollment) != nil || enrollment.Version != 1 {
		return ComposePlan{}, errors.New("图形组件尚未接管；请使用包含图形部署能力的安装/升级脚本")
	}
	if enrollment.DriveEnabled != "true" && enrollment.DriveEnabled != "false" {
		return ComposePlan{}, errors.New("图形共享盘部署记录无效")
	}
	server, graphics := model.Services["server"], model.Services["guacd"]
	if server == nil || graphics == nil {
		return ComposePlan{}, errors.New("已接管的 server/guacd 服务定义缺失")
	}
	var labels map[string]string
	var profiles []string
	var image string
	var health struct {
		Test    []string
		Disable bool
	}
	if json.Unmarshal(graphics["labels"], &labels) != nil || labels[graphicsOwner] != "v1" {
		return ComposePlan{}, errors.New("guacd 不是已接管组件，拒绝改变外部或自定义服务")
	}
	if json.Unmarshal(graphics["image"], &image) != nil || image == "" || strings.HasSuffix(image, ":latest") || strings.HasSuffix(image, ":") || strings.LastIndex(image, ":") <= strings.LastIndex(image, "/") {
		return ComposePlan{}, errors.New("图形组件必须使用明确版本或摘要的镜像")
	}
	if json.Unmarshal(graphics["healthcheck"], &health) != nil || health.Disable || len(health.Test) < 2 || health.Test[0] == "NONE" {
		return ComposePlan{}, errors.New("图形组件缺少有效健康检查")
	}
	if value := graphics["profiles"]; value != nil {
		if json.Unmarshal(value, &profiles) != nil || len(profiles) > 1 || len(profiles) == 1 && profiles[0] != graphicsProfile {
			return ComposePlan{}, errors.New("自定义图形 profiles 不支持自动部署")
		}
	}
	for _, service := range []map[string]json.RawMessage{server, graphics} {
		var mode string
		if value := service["network_mode"]; value != nil && (json.Unmarshal(value, &mode) != nil || mode != "") {
			return ComposePlan{}, errors.New("图形自动部署不支持 network_mode")
		}
		var networks map[string]json.RawMessage
		if json.Unmarshal(service["networks"], &networks) != nil || networks["graphics"] == nil {
			return ComposePlan{}, errors.New("server 和 guacd 必须接入已登记的 graphics 网络")
		}
	}
	var networks map[string]struct {
		Internal bool
		External bool
	}
	if json.Unmarshal(doc["networks"], &networks) != nil || !networks["graphics"].Internal || networks["graphics"].External {
		return ComposePlan{}, errors.New("图形网络必须是内部且由当前项目管理")
	}
	var ports []json.RawMessage
	if value := graphics["ports"]; value != nil && (json.Unmarshal(value, &ports) != nil || len(ports) > 0) {
		return ComposePlan{}, errors.New("guacd 不允许映射宿主机端口")
	}
	for name, service := range model.Services {
		if name == "guacd" {
			continue
		}
		var deps map[string]json.RawMessage
		if value := service["depends_on"]; value != nil {
			if json.Unmarshal(value, &deps) != nil || deps["guacd"] != nil {
				return ComposePlan{}, errors.New("存在依赖 guacd 的自定义服务，需要先解除部署依赖")
			}
		}
	}
	var env map[string]json.RawMessage
	if json.Unmarshal(server["environment"], &env) != nil || env == nil {
		return ComposePlan{}, errors.New("图形后端环境配置无效")
	}
	var addr, relay string
	json.Unmarshal(env["NT_GUACD_ADDR"], &addr)
	json.Unmarshal(env["NT_GUACD_RELAY_HOST"], &relay)
	if addr != "" && addr != "guacd:4822" || relay != "" && relay != "server" {
		return ComposePlan{}, errors.New("外部 guacd 或自定义中继地址不属于自动部署范围")
	}
	if r.Enabled {
		delete(graphics, "profiles")
		env["NT_GUACD_ADDR"] = json.RawMessage(`"guacd:4822"`)
		env["NT_GUACD_RELAY_HOST"] = json.RawMessage(`"server"`)
		env["NT_RDP_DRIVE_ENABLED"], _ = json.Marshal(enrollment.DriveEnabled)
	} else {
		graphics["profiles"], _ = json.Marshal([]string{graphicsProfile})
		env["NT_GUACD_ADDR"] = json.RawMessage(`""`)
		env["NT_GUACD_RELAY_HOST"] = json.RawMessage(`""`)
		env["NT_RDP_DRIVE_ENABLED"] = json.RawMessage(`"false"`)
	}
	env["NT_DEPLOY_MANAGED_GRAPHICS"], _ = json.Marshal(map[bool]string{true: "true", false: "false"}[r.Enabled])
	server["environment"], _ = json.Marshal(env)
	doc["services"], _ = json.Marshal(model.Services)
	result, err := json.Marshal(doc)
	return ComposePlan{Document: result}, err
}

func graphicsEnabled(raw []byte) bool {
	var model composeModel
	var env map[string]string
	if json.Unmarshal(raw, &model) != nil || json.Unmarshal(model.Services["server"]["environment"], &env) != nil {
		return false
	}
	return env["NT_GUACD_ADDR"] != ""
}

func checkGraphicsDevices(raw []byte) error {
	var model composeModel
	if json.Unmarshal(raw, &model) != nil {
		return errors.New("图形部署配置无法解析")
	}
	var devices []struct {
		Source string `json:"source"`
	}
	if value := model.Services["guacd"]["devices"]; value != nil {
		if json.Unmarshal(value, &devices) != nil {
			return errors.New("图形设备挂载格式无效")
		}
		for _, d := range devices {
			if d.Source != "/dev/fuse" {
				return errors.New("图形组件包含不支持自动检查的设备")
			}
			info, err := os.Stat(d.Source)
			if err != nil || info.Mode()&os.ModeDevice == 0 {
				return errors.New("图形组件已配置共享盘，但宿主 /dev/fuse 不可用")
			}
		}
	}
	return nil
}

func (e ComposeExecutor) applyGraphics(ctx context.Context, enabled bool) error {
	if err := e.Validate(ctx); err != nil {
		return err
	}
	if enabled {
		if _, err := e.run(ctx, 4*time.Minute, "pull graphics", "pull", "--policy", "missing", "guacd"); err != nil {
			return err
		}
	}
	// Recreate the backend first to close old sessions and change admission.
	// On disable, stop guacd even if backend replacement fails (fail closed).
	_, serverErr := e.run(ctx, 3*time.Minute, "apply graphics admission", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "server")
	if !enabled {
		if err := e.stopGraphics(ctx); err != nil {
			return err
		}
		if serverErr != nil {
			return serverErr
		}
	} else {
		if serverErr != nil {
			return serverErr
		}
		if _, err := e.run(ctx, 3*time.Minute, "start graphics", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "guacd"); err != nil {
			return err
		}
	}
	_, err := e.run(ctx, 3*time.Minute, "refresh web", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "web")
	return err
}

func (e ComposeExecutor) stopGraphics(ctx context.Context) error {
	// Explicitly name the profiled service; never remove a volume or another service.
	if _, err := e.run(ctx, 45*time.Second, "stop graphics", "stop", "--timeout", "15", "guacd"); err != nil {
		return err
	}
	return nil
}

func (e ComposeExecutor) verifyGraphics(ctx context.Context, enabled bool) error {
	raw, err := e.run(ctx, 30*time.Second, "verify graphics", "ps", "--all", "--format", "json", "server", "web", "guacd")
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
		if c.Service == "server" || c.Service == "web" || c.Service == "guacd" && enabled {
			if c.State != "running" || c.Health != "healthy" {
				return errors.New("应用或图形组件未通过健康检查")
			}
		}
		if c.Service == "guacd" {
			if !enabled && c.State != "exited" && c.State != "created" {
				return errors.New("图形组件仍在运行")
			}
			for _, p := range c.Publishers {
				if p.PublishedPort != 0 {
					return errors.New("图形组件存在不允许的宿主端口")
				}
			}
		}
	}
	if counts["server"] != 1 || counts["web"] != 1 || enabled && counts["guacd"] != 1 || counts["guacd"] > 1 {
		return errors.New("图形组件实例数量不符合单机部署约束")
	}
	return nil
}

func parseGraphicsContainers(raw []byte) ([]containerStatus, error) {
	var err error
	var list []containerStatus
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		if json.Unmarshal(raw, &list) != nil {
			return nil, errors.New("图形组件状态无法解析")
		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var c containerStatus
			err = decoder.Decode(&c)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.New("图形组件状态无法解析")
			}
			list = append(list, c)
		}
	}
	return list, nil
}

type ComponentStatus struct {
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observedAt"`
	Supported  bool      `json:"supported"`
	Enabled    bool      `json:"enabled"`
	Reason     string    `json:"reason,omitempty"`
}

func (d *HostDriver) Components(ctx context.Context) map[string]ComponentStatus {
	result := ComponentStatus{State: "unknown", ObservedAt: time.Now().UTC(), Reason: "组件部署配置不可读取"}
	if len(d.Executor.Files) != 1 {
		return d.withMP4Component(ctx, nil, d.withDriveComponent(ctx, nil, result))
	}
	raw, err := os.ReadFile(d.Executor.Files[0])
	if err == nil {
		result.Enabled = graphicsEnabled(raw)
		_, err = planGraphics(raw, Request{Protocol: "graphics", Enabled: result.Enabled})
		if err == nil {
			result.Supported = true
			result.Reason = "组件运行状态无法读取"
			observed, queryErr := d.Executor.run(ctx, 2*time.Second, "observe graphics", "ps", "--all", "--format", "json", "guacd")
			if queryErr == nil {
				containers, parseErr := parseGraphicsContainers(observed)
				if parseErr == nil {
					result.State, result.Reason = "stopped", "图形组件容器已停止或尚未创建"
					if len(containers) > 1 {
						result.State, result.Reason = "failed", "图形组件实例数量不符合单机部署约束"
					}
					if len(containers) == 1 {
						c := containers[0]
						if c.Service != "guacd" || c.State != "exited" && c.State != "created" {
							result.State, result.Reason = "failed", "图形组件未通过运行健康检查"
							if c.Service == "guacd" && c.State == "running" && c.Health == "healthy" {
								result.State, result.Reason = "running", "图形组件容器健康"
							}
						}
						for _, port := range c.Publishers {
							if port.PublishedPort != 0 {
								result.State, result.Reason = "failed", "图形组件存在不允许的宿主端口"
							}
						}
					}
				}
			}
		} else {
			result.Reason = err.Error()
		}
	}
	return d.withMP4Component(ctx, raw, d.withDriveComponent(ctx, raw, result))
}
