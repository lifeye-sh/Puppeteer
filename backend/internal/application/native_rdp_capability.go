package application

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/configstate"
	"puppeteer/backend/internal/platform/listener"
)

type nativeRDPCapacity struct {
	Limit     int `json:"limit"`
	InUse     int `json:"inUse"`
	Available int `json:"available"`
}
type nativeRDPCapability struct {
	Supported   bool                `json:"supported"`
	StaticReady bool                `json:"staticReady"`
	Profile     string              `json:"profile"`
	Capacity    nativeRDPCapacity   `json:"capacity"`
	Checks      []configstate.Check `json:"checks"`
	ObservedAt  time.Time           `json:"observedAt"`
	Evidence    string              `json:"-"`
}

// Inspection never starts a worker, binds a socket or returns deployment paths,
// certificate contents or private-key errors. Executable probes are explicit.
func (a *App) inspectNativeRDP(v system.ConfigurationValues) nativeRDPCapability {
	now := time.Now().UTC()
	r := nativeRDPCapability{Profile: "unavailable", StaticReady: true, ObservedAt: now, Checks: []configstate.Check{}}
	proof := sha256.New()
	add := func(code string, ok bool, yes, no, remedy string) {
		state, summary := "ready", yes
		if !ok {
			state, summary = "blocked", no
			r.StaticReady = false
		}
		r.Checks = append(r.Checks, configstate.Check{Code: code, State: state, Summary: summary, Remediation: remedy, Scope: "node-rdp-capability", CheckedAt: now})
	}
	add("rdp_platform", runtime.GOOS == "linux", "当前节点为 Linux", "原生录屏 RDP 需要 Linux 节点", "")
	o := a.managedRDP
	add("rdp_deployment", o != nil, "已登记原生 RDP 部署配置", "当前节点未部署原生 RDP Worker 接入配置", "使用专用原生 RDP 部署；普通生产镜像的开关不能安装 Worker")
	if o == nil {
		r.Evidence = hex.EncodeToString(proof.Sum(nil))
		return r
	}
	r.Supported = runtime.GOOS == "linux"
	r.Profile = "loopback"
	if o.ContainerProfile {
		r.Profile = "windows-local"
	}
	fmt.Fprintf(proof, "%+v", *o)
	add("rdp_runtime", !a.maintenance.Load(), "节点未处于恢复隔离或维护状态", "当前节点处于恢复隔离或维护状态", "完成恢复或维护后重新预检")
	address := v.String("native-rdp-addr")
	if address == "" {
		address = o.Address
	}
	host, port, err := net.SplitHostPort(address)
	number, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	add("rdp_listener_scope", err == nil && portErr == nil && number >= 0 && number <= 65535 && ip != nil && (o.ContainerProfile || ip.IsLoopback()), "监听地址符合当前部署限制", "监听地址无效，或普通部署使用了非回环地址", "普通部署仅限回环；windows-local 显式 profile 的宿主映射仍须保持回环")
	for _, item := range []struct{ code, path, label string }{{"rdp_worker", o.Worker, "RDP Worker"}, {"rdp_ffmpeg", o.FFmpeg, "FFmpeg"}} {
		info, e := os.Stat(item.path)
		ok := filepath.IsAbs(item.path) && e == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
		add(item.code, ok, item.label+" 文件可执行；运行能力由显式预检确认", item.label+" 缺失、路径非绝对路径或不可执行", "安装匹配版本并检查运行用户权限")
		if e == nil {
			fmt.Fprintf(proof, "%s:%d:%d:%d", item.code, info.Size(), info.ModTime().UnixNano(), info.Mode())
		}
	}
	read := func(path string) []byte {
		if !filepath.IsAbs(path) {
			return nil
		}
		// Reject pipes/devices before Open so a mistaken deployment path cannot
		// block a read-only status request waiting for a writer.
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return nil
		}
		defer f.Close()
		info, e = f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return nil
		}
		b, e := io.ReadAll(io.LimitReader(f, 65537))
		if e != nil || len(b) > 65536 {
			clear(b)
			return nil
		}
		return b
	}
	cert, key := read(o.Certificate), read(o.PrivateKey)
	defer clear(key)
	pair, e := tls.X509KeyPair(cert, key)
	valid := e == nil
	add("rdp_tls_identity", valid, "TLS 证书与私钥匹配", "TLS 证书或私钥不可读、格式无效或不匹配", "检查部署证书和私钥；页面不接受私钥路径或内容")
	proof.Write(cert)
	fmt.Fprintf(proof, "tls=%t", valid)
	endpoint := v.String("native-rdp-public-address")
	if endpoint == "" {
		endpoint = o.Advertised
	}
	if endpoint == "" {
		endpoint = address
	}
	fmt.Fprintf(proof, "%s/%s", address, endpoint)
	if valid {
		leaf, e := x509.ParseCertificate(pair.Certificate[0])
		name, _, splitErr := net.SplitHostPort(endpoint)
		covered := e == nil && splitErr == nil && leaf.VerifyHostname(name) == nil
		add("rdp_tls_endpoint", covered, "证书覆盖客户端访问地址", "证书未覆盖客户端访问地址或访问地址格式无效", "配置匹配的客户端地址和服务端证书")
		add("rdp_tls_validity", e == nil && !now.Before(leaf.NotBefore) && now.Before(leaf.NotAfter), "证书处于有效期", "证书尚未生效或已过期", "更新证书后重新预检")
	}
	r.Capacity = nativeRDPCapacity{Limit: cap(a.managedRDPSlots), InUse: len(a.managedRDPSlots)}
	r.Capacity.Available = r.Capacity.Limit - r.Capacity.InUse
	add("rdp_capacity", r.Capacity.Available > 0, fmt.Sprintf("会话容量 %d，当前占用 %d", r.Capacity.Limit, r.Capacity.InUse), "原生 RDP 会话容量已满或未初始化", "等待现有会话结束；预检不会占用或预留会话名额")
	r.Evidence = hex.EncodeToString(proof.Sum(nil))
	return r
}

type rdpProbeOutput struct {
	data     []byte
	overflow bool
}

func (b *rdpProbeOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (256 << 10) - len(b.data)
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func nativeRDPExecutableChecks(ctx context.Context, o NativeRDPOptions) []configstate.Check {
	checks := []configstate.Check{}
	for _, probe := range []struct {
		code, path string
		args       []string
	}{{"rdp_worker_protocol", o.Worker, []string{"--capabilities"}}, {"rdp_ffmpeg_encoder", o.FFmpeg, []string{"-hide_banner", "-encoders"}}} {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		cmd := exec.CommandContext(c, probe.path, probe.args...)
		cmd.WaitDelay = 100 * time.Millisecond
		out := &rdpProbeOutput{}
		cmd.Stdout = out
		cmd.Stderr = io.Discard
		err := cmd.Run()
		cancel()
		ok := err == nil && !out.overflow
		summary, remedy := "Worker 能力协议与只读录屏模式验证通过", "更新匹配的 Worker；检查执行权限和动态库依赖"
		if probe.code == "rdp_worker_protocol" {
			var v struct {
				Version      int
				RecordedView bool
			}
			ok = ok && json.Unmarshal(out.data, &v) == nil && v.Version == 1 && v.RecordedView
			if !ok {
				summary = "Worker 无法运行或不支持当前能力检测协议"
			}
		} else {
			ok = ok && strings.Contains(string(out.data), " libx264 ")
			summary = "FFmpeg 可运行且提供 libx264 编码器"
			remedy = "安装包含 libx264 的 FFmpeg 并检查执行权限"
			if !ok {
				summary = "FFmpeg 无法运行或缺少 libx264 编码器"
			}
		}
		state := "ready"
		if !ok {
			state = "blocked"
		}
		checks = append(checks, configstate.Check{Code: probe.code, State: state, Summary: summary, Remediation: remedy, Scope: "node-rdp-executable", CheckedAt: time.Now().UTC()})
	}
	return checks
}

func (a *App) runNativeRDPCheck(ctx context.Context, t configstate.Task, v system.ConfigurationValues) {
	initial := a.inspectNativeRDP(v)
	t.Evidence = initial.Evidence
	t.Checks = initial.Checks
	t.Stage = "capability"
	t.Updated = time.Now().UTC()
	if !a.storeConfigurationCheck(ctx, t) {
		return
	}
	if initial.StaticReady {
		t.Checks = append(t.Checks, nativeRDPExecutableChecks(ctx, *a.managedRDP)...)
		t.Stage = "listener"
		t.Updated = time.Now().UTC()
		if !a.storeConfigurationCheck(ctx, t) {
			return
		}
		a.configurationMu.Lock()
		manager := a.listenerManager
		candidate := system.ConfigurationValues{}
		for k, value := range v {
			candidate[k] = value
		}
		candidate["native-rdp-enabled"] = true
		spec, e := a.managedRDPSpec(candidate)
		if e == nil && spec != nil && manager != nil {
			var prepared *listener.Prepared
			prepared, e = manager.Prepare(ctx, []listener.Spec{*spec})
			if prepared != nil {
				prepared.Abort()
			}
		}
		a.configurationMu.Unlock()
		state, summary := "ready", "当前进程监听地址可用；临时预留已释放，不代表宿主映射或公网可达"
		if e != nil || spec == nil || manager == nil {
			state, summary = "blocked", "RDP 监听管理器未就绪、依赖变化或端口被占用"
		}
		t.Checks = append(t.Checks, configstate.Check{Code: "rdp_port", State: state, Summary: summary, Remediation: "检查当前进程监听地址和端口占用；本预检不操作宿主端口", Scope: "process-listener"})
	}
	latest, e := a.system.Configuration(ctx, 0)
	current := a.inspectNativeRDP(v)
	if e != nil || latest.ID != t.Revision || current.Evidence != initial.Evidence {
		t.Checks = append(t.Checks, configstate.Check{Code: "rdp_inputs_changed", State: "blocked", Summary: "预检期间配置或部署依赖已变化，请重新检测", Scope: "node"})
	}
	if initial.StaticReady && !current.StaticReady {
		t.Checks = append(t.Checks, configstate.Check{Code: "rdp_readiness_changed", State: "blocked", Summary: "预检期间容量、证书或运行状态已变化", Scope: "node"})
	}
	t.State = "succeeded"
	for _, c := range t.Checks {
		if c.State != "ready" {
			t.State = "failed"
		}
	}
	t.Stage = "complete"
	t.Updated = time.Now().UTC()
	t.Expires = t.Updated.Add(5 * time.Minute)
	a.storeConfigurationCheck(ctx, t)
}
