package deploytask

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

var errRendererDemandDisabled = errors.New("MP4 按需运行尚未启用")

func rendererSettings(raw []byte) (mode, quality string) {
	mode, quality = "always", "balanced"
	var m composeModel
	var env, ce map[string]string
	_ = json.Unmarshal(raw, &m)
	_ = json.Unmarshal(m.Services["server"]["environment"], &env)
	_ = json.Unmarshal(m.Services["mp4-renderer"]["environment"], &ce)
	if env["NT_MP4_ON_DEMAND"] == "true" {
		mode = "on-demand"
	}
	if ce["NT_MP4_QUALITY"] != "" {
		quality = ce["NT_MP4_QUALITY"]
	}
	return
}

func (e ComposeExecutor) checkRendererCapabilities(ctx context.Context, raw []byte, mode, quality string) error {
	var m composeModel
	_ = json.Unmarshal(raw, &m)
	required := map[string]string{"mp4-renderer": "io.puppeteer.renderer.lifecycle"}
	if mode == "on-demand" {
		required["server"] = "io.puppeteer.mp4.ondemand"
	}
	for service, label := range required {
		var image string
		_ = json.Unmarshal(m.Services[service]["image"], &image)
		out, err := e.execute(ctx, 5*time.Second, "check MP4 runtime capabilities", "image", "inspect", image, "--format", `{{index .Config.Labels `+strconv.Quote(label)+`}}`)
		if err != nil || strings.TrimSpace(string(out)) != "v1" {
			return errors.New("MP4 按需运行及质量配置需要匹配的新应用/渲染器镜像，请先升级")
		}
	}
	return nil
}

func (d *HostDriver) demandConfig() ([]byte, error) {
	if len(d.Executor.Files) != 1 {
		return nil, errors.New("MP4 按需运行需要已接管的单机部署")
	}
	raw, err := os.ReadFile(d.Executor.Files[0])
	if err != nil {
		return nil, err
	}
	mode, _ := rendererSettings(raw)
	if mode != "on-demand" || !mp4Enabled(raw) {
		return nil, errRendererDemandDisabled
	}
	if _, err = planMP4(raw, Request{Protocol: "mp4", Enabled: true}); err != nil {
		return nil, err
	}
	return raw, nil
}

func (d *HostDriver) RendererTouch(ctx context.Context) error {
	if _, err := d.demandConfig(); err != nil {
		return err
	}
	d.rendererLeaseUntil.Store(time.Now().Add(5 * time.Minute).UnixNano())
	return nil
}

func (d *HostDriver) RendererWake(ctx context.Context) error {
	raw, err := d.demandConfig()
	if err != nil {
		return err
	}
	mode, quality := rendererSettings(raw)
	if err = d.Executor.checkRendererCapabilities(ctx, raw, mode, quality); err != nil {
		return err
	}
	d.rendererLeaseUntil.Store(time.Now().Add(5 * time.Minute).UnixNano())
	// No application/web recreation, no pull, no change to the managed recipe.
	if _, err = d.Executor.run(ctx, 30*time.Second, "wake MP4 renderer", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--no-recreate", "--wait", "--wait-timeout", "25", "mp4-renderer"); err != nil {
		return err
	}
	_, err = d.Executor.rendererControl(ctx, "resume")
	return err
}

func (e ComposeExecutor) rendererControl(ctx context.Context, operation string) (bool, error) {
	if operation != "resume" && operation != "drain" {
		return false, errors.New("invalid renderer runtime operation")
	}
	script := `const fs=require('node:fs');const token=fs.readFileSync('/run/secrets/mp4_token','utf8').trim();fetch('http://127.0.0.1:8090/runtime/` + operation + `',{method:'POST',headers:{Authorization:'Bearer '+token},signal:AbortSignal.timeout(4000)}).then(async r=>{if(!r.ok)throw Error('runtime');console.log(JSON.stringify(await r.json()))}).catch(()=>process.exit(1));`
	out, err := e.run(ctx, 7*time.Second, "check MP4 idle admission", "exec", "-T", "mp4-renderer", "node", "-e", script)
	if err != nil {
		return false, err
	}
	var v struct{ Ready, Drained bool }
	if json.Unmarshal(out, &v) != nil {
		return false, errors.New("invalid MP4 runtime response")
	}
	if operation == "resume" && !v.Ready {
		return false, errors.New("MP4 renderer did not resume")
	}
	return v.Drained, nil
}

func (d *HostDriver) idleRenderer(ctx context.Context) error {
	if _, err := d.demandConfig(); err != nil {
		if errors.Is(err, errRendererDemandDisabled) {
			return nil
		}
		return err
	} // Disabled/legacy recipes stay untouched.
	now := time.Now()
	if now.UnixNano() < d.rendererLeaseUntil.Load() {
		return nil
	}
	out, err := d.Executor.run(ctx, 5*time.Second, "observe idle renderer", "ps", "--format", "json", "mp4-renderer")
	if err != nil {
		return err
	}
	list, err := parseGraphicsContainers(out)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	if len(list) != 1 || list[0].State != "running" || list[0].Health != "healthy" {
		return nil
	}
	drained, err := d.Executor.rendererControl(ctx, "drain")
	if err != nil || !drained {
		return err
	}
	_, err = d.Executor.run(ctx, 25*time.Second, "sleep idle MP4 renderer", "stop", "--timeout", "15", "mp4-renderer")
	if err != nil {
		_, _ = d.Executor.rendererControl(ctx, "resume")
	}
	return err
}

func (d *HostDriver) RunRendererMaintenance(ctx context.Context, e *Engine) {
	// Start the recovery grace at daemon startup, not at the first idle tick
	// that manages to acquire the deployment gate. The renderer independently
	// enforces its own five-minute idle window after startup and each job.
	d.rendererLeaseUntil.CompareAndSwap(0, time.Now().Add(5*time.Minute).UnixNano())
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			err := e.Maintain(ctx, d.idleRenderer)
			if errors.Is(err, ErrBusy) || ctx.Err() != nil {
				continue
			}
			if err != nil {
				message := err.Error()
				d.rendererMaintenanceError.Store(&message)
				slog.Warn("MP4 idle maintenance failed", "reason", message)
			} else {
				d.rendererMaintenanceError.Store(nil)
			}
		}
	}
}
