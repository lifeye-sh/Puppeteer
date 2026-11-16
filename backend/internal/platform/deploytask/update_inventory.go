package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"puppeteer/backend/internal/platform/buildmeta"
	"runtime"
	"strings"
	"time"
)

type UpdateComponent struct {
	Service         string `json:"service"`
	ConfiguredImage string `json:"configuredImage"`
	RunningImage    string `json:"runningImage,omitempty"`
	ImageID         string `json:"imageId,omitempty"`
	Version         string `json:"version,omitempty"`
	State           string `json:"state"`
	Health          string `json:"health,omitempty"`
	Disabled        bool   `json:"disabled"`
}
type UpdateInventory struct {
	ExecutorVersion string            `json:"executorVersion"`
	Platform        string            `json:"platform"`
	Managed         bool              `json:"managed"`
	Systemd         bool              `json:"systemd"`
	RunnerReady     bool              `json:"runnerReady"`
	Components      []UpdateComponent `json:"components"`
	Reason          string            `json:"reason,omitempty"`
}

func (d *HostDriver) UpdateInventory(ctx context.Context) UpdateInventory {
	out := UpdateInventory{ExecutorVersion: buildmeta.Release, Platform: runtime.GOOS + "/" + runtime.GOARCH, Components: []UpdateComponent{}, Reason: "独立升级器尚未接入；当前请使用受管升级脚本"}
	pid1, _ := os.ReadFile("/proc/1/comm")
	_, unitErr := os.Stat("/run/systemd/system")
	out.Systemd = unitErr == nil && strings.TrimSpace(string(pid1)) == "systemd"
	// Inspect only an enrolled local installation. Never infer host systemd from
	// the backend container's OS, and never expose resolved environment secrets.
	if len(d.Executor.Files) != 1 || filepath.Base(d.Executor.Files[0]) != "compose-managed.json" {
		out.Reason = "当前 Compose 布局尚未受管"
		return out
	}
	raw, e := os.ReadFile(d.Executor.Files[0])
	var model composeModel
	if e != nil || json.Unmarshal(raw, &model) != nil {
		out.Reason = "无法读取受管部署配置"
		return out
	}
	out.Managed = true
	if d.UpgradeAvailable != nil {
		out.RunnerReady, out.Reason = d.UpgradeAvailable()
	}
	for _, service := range []string{"server", "web", "guacd", "mp4-renderer", "postgres"} {
		item, ok := model.Services[service]
		if !ok {
			continue
		}
		c := UpdateComponent{Service: service, State: "unknown"}
		json.Unmarshal(item["image"], &c.ConfiguredImage)
		var profiles []string
		json.Unmarshal(item["profiles"], &profiles)
		for _, p := range profiles {
			if strings.HasPrefix(p, "puppeteer-") && strings.HasSuffix(p, "-disabled") {
				c.Disabled = true
			}
		}
		id, e := d.Executor.run(ctx, 3*time.Second, "observe update component", "ps", "-q", "--all", service)
		if e == nil && len(strings.TrimSpace(string(id))) >= 12 && !strings.Contains(strings.TrimSpace(string(id)), "\n") {
			format := `{"runningImage":{{json .Config.Image}},"imageId":{{json .Image}},"version":{{json (index .Config.Labels "org.opencontainers.image.version")}},"state":{{json .State.Status}},"health":{{if .State.Health}}{{json .State.Health.Status}}{{else}}""{{end}}}`
			observed, err := d.Executor.execute(ctx, 3*time.Second, "inspect update component", "inspect", "--format", format, strings.TrimSpace(string(id)))
			if err == nil {
				json.Unmarshal(observed, &c)
			}
		} else if e == nil {
			c.State = "not_created"
		}
		out.Components = append(out.Components, c)
	}
	return out
}
