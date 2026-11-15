package deploytask

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func specialized(p string) bool { return p == "agent-repository" || p == "wal" || p == "web-access" }
func validateSpecialized(r Request) error {
	if r.SiteID != "" || r.SiteVersion != 0 || r.SiteEnabled || r.Bind != "" || r.Port != 0 {
		return errors.New("专项运维不接受端口或网站参数")
	}
	if r.Protocol != "web-access" && (r.AllowCIDRs != "" || r.AdminIP != "") {
		return errors.New("仅入口策略接受地址范围")
	}
	if r.Protocol != "agent-repository" && (r.Repository != "" || r.TrustRoot != "") {
		return errors.New("仅更新仓库接受签名源")
	}
	if r.Protocol == "web-access" && r.Enabled {
		_, err := accessPolicy(r)
		return err
	}
	if r.Protocol == "agent-repository" && r.Enabled {
		u, e := url.Parse(r.Repository)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(r.Repository) > 2048 || len(r.TrustRoot) > 32768 || !json.Valid([]byte(r.TrustRoot)) {
			return errors.New("需要 HTTPS 仓库地址及有效的可信根 JSON")
		}
	}
	return nil
}

func accessPolicy(r Request) ([]byte, error) {
	if !r.Enabled {
		return []byte("# managed access policy disabled\n"), nil
	}
	ip, e := netip.ParseAddr(r.AdminIP)
	if e != nil || ip.IsUnspecified() {
		return nil, errors.New("无法确认管理员来源 IP，未修改入口策略")
	}
	ip = ip.Unmap()
	parts := strings.FieldsFunc(r.AllowCIDRs, func(c rune) bool { return c == ',' || c == '\n' || c == '\r' || c == ' ' })
	if len(parts) == 0 || len(parts) > 64 || len(r.AllowCIDRs) > 4096 {
		return nil, errors.New("请配置 1 到 64 个允许访问的 CIDR 网段")
	}
	allowed := false
	policy := "# managed by puppeteer-deployd; loopback is the recovery channel\nallow 127.0.0.1;\nallow ::1;\n"
	seen := map[string]bool{}
	for _, part := range parts {
		p, e := netip.ParsePrefix(part)
		if e != nil || p != p.Masked() || p.Addr().Is4In6() || p.Addr().IsMulticast() {
			return nil, errors.New("访问范围必须是规范的 IPv4/IPv6 CIDR")
		}
		allowed = allowed || p.Contains(ip)
		if !seen[p.String()] {
			policy += "allow " + p.String() + ";\n"
			seen[p.String()] = true
		}
	}
	if !allowed {
		return nil, errors.New("允许范围不包含当前管理员 IP，已阻止可能锁定管理员的操作")
	}
	return []byte(policy + "deny all;\n"), nil
}

type operationsEnrollment struct {
	Version int  `json:"version"`
	WAL     bool `json:"wal"`
}
type operationsState struct {
	Digest     string    `json:"digest"`
	Enabled    bool      `json:"enabled"`
	Generation uint64    `json:"generation"`
	CheckedAt  time.Time `json:"checkedAt"`
	State      string    `json:"state"`
	Reason     string    `json:"reason,omitempty"`
}

func (d *HostDriver) opsStage(stage string) error {
	if d.Progress != nil {
		return d.Progress(stage)
	}
	return nil
}
func (d *HostDriver) opsDocument(ctx context.Context, r Request) ([]byte, error) {
	if len(d.Executor.Files) != 1 || d.Check == nil {
		return nil, errors.New("专项运维需要已接管的单机部署执行器")
	}
	if err := Validate(r); err != nil {
		return nil, err
	}
	if err := d.Check(ctx, r); err != nil {
		return nil, err
	}
	raw, e := d.Executor.Resolve(ctx)
	if e != nil {
		return nil, e
	}
	var doc map[string]json.RawMessage
	var enrolled operationsEnrollment
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(doc["x-puppeteer-operations"], &enrolled) != nil || enrolled.Version != 1 {
		return nil, errors.New("专项运维尚未接管，请更新匹配的安装/升级脚本")
	}
	if r.Protocol == "wal" && !enrolled.WAL {
		return nil, errors.New("当前 PostgreSQL 部署不满足受管 PG18 WAL 条件")
	}
	return raw, nil
}

func (d *HostDriver) specializedPreflight(ctx context.Context, r Request) error {
	raw, e := d.opsDocument(ctx, r)
	if e != nil {
		return e
	}
	if r.Protocol == "wal" {
		return d.walPreflight(ctx, raw, r)
	}
	plan, e := d.opsWebPlan(ctx, raw, r)
	if e != nil {
		return e
	}
	defer d.cleanupRepositoryPlan(plan, r)
	return d.validateOpsPlan(ctx, plan)
}

func (d *HostDriver) validateOpsPlan(ctx context.Context, plan ComposePlan) error {
	if e := d.prepareAuxiliary(plan); e != nil {
		return e
	}
	raw, e := literalCompose(plan.Document)
	if e != nil {
		return e
	}
	file, e := os.CreateTemp(d.StateDirectory, ".operations-*.json")
	if e != nil {
		return e
	}
	path := file.Name()
	defer os.Remove(path)
	_, e = file.Write(raw)
	ce := file.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	candidate := d.Executor
	candidate.Files = []string{path}
	if e = candidate.Validate(ctx); e != nil {
		return e
	}
	return candidate.ValidateNginx(ctx)
}

func (d *HostDriver) specializedApply(ctx context.Context, r Request) error {
	// Persist the recovery boundary before downloads, builds or application checks.
	state, e := d.load()
	if e != nil {
		return e
	}
	prior, e := os.ReadFile(d.Executor.Files[0])
	if e != nil {
		return e
	}
	state.Previous, state.PreviousOwned, state.Request = prior, state.Owned, &r
	if e = d.save(state); e != nil {
		return e
	}
	raw, e := d.opsDocument(ctx, r)
	if e != nil {
		return e
	}
	var plan ComposePlan
	if r.Protocol == "wal" {
		plan, e = d.walPlan(ctx, raw, r)
	} else {
		plan, e = d.opsWebPlan(ctx, raw, r)
	}
	if e != nil {
		return e
	}
	if r.Protocol != "wal" {
		if e = d.validateOpsPlan(ctx, plan); e != nil {
			return e
		}
	} else if e = d.walPrepare(ctx, raw, r); e != nil {
		return e
	}
	if e = d.prepareAuxiliary(plan); e != nil {
		return e
	}
	literal, e := literalCompose(plan.Document)
	if e != nil {
		return e
	}
	if e = atomicPrivate(d.Executor.Files[0], literal); e != nil {
		return e
	}
	if r.Protocol == "wal" {
		return d.walApply(ctx, r)
	}
	if e = d.opsStage("apply_nginx"); e != nil {
		return e
	}
	return d.applyWeb(ctx)
}

func (d *HostDriver) applyWeb(ctx context.Context) error {
	if e := d.Executor.Validate(ctx); e != nil {
		return e
	}
	_, e := d.Executor.run(ctx, 3*time.Minute, "apply web operations", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "web")
	return e
}

func (d *HostDriver) specializedVerify(ctx context.Context, r Request) error {
	if e := d.opsStage("verify_operations"); e != nil {
		return e
	}
	if r.Protocol == "wal" {
		return d.walVerify(ctx, r)
	}
	if e := d.Executor.ValidateNginx(ctx); e != nil {
		return e
	}
	if e := d.verifyWebEntry(ctx); e != nil {
		return e
	}
	if r.Protocol == "agent-repository" {
		if e := d.verifyRepository(ctx, r); e != nil {
			return e
		}
	}
	// Record scope explicitly: host-local HTTPS verification is not public reachability.
	return d.saveOpsState(r, "verified", "Nginx 配置及本机 HTTPS 入口已验证；外部网络不在自动化管理范围")
}

func (d *HostDriver) specializedRecover(ctx context.Context, r Request, fence bool) error {
	state, e := d.load()
	if e != nil {
		return e
	}
	if state.Request == nil || *state.Request != r || len(state.Previous) == 0 {
		return errors.New("专项部署恢复快照缺失；未猜测或覆盖当前配置")
	}
	if fence {
		// Recovery cannot depend on an API which may be restarting or unavailable.
		raw, err := d.Executor.Resolve(ctx)
		if err != nil {
			return err
		}
		var plan ComposePlan
		if r.Protocol == "wal" {
			plan, err = d.walPlan(ctx, raw, r)
		} else {
			plan, err = d.opsWebPlan(ctx, raw, r)
		}
		if err != nil {
			return err
		}
		if err = d.prepareAuxiliary(plan); err != nil {
			return err
		}
		literal, err := literalCompose(plan.Document)
		if err != nil {
			return err
		}
		if err = atomicPrivate(d.Executor.Files[0], literal); err != nil {
			return err
		}
		if r.Protocol == "wal" {
			err = d.walRestart(ctx)
		} else {
			err = d.applyWeb(ctx)
		}
		if err != nil {
			return err
		}
		return d.specializedVerify(ctx, r)
	}
	if e = atomicPrivate(d.Executor.Files[0], state.Previous); e != nil {
		return e
	}
	if r.Protocol == "wal" {
		e = d.walRestart(ctx)
	} else {
		e = d.applyWeb(ctx)
	}
	if e != nil {
		return e
	}
	return d.saveOpsState(r, "rolled_back", "专项部署失败，已恢复原配置；备份数据及签名发布文件保留")
}
func (d *HostDriver) saveOpsState(r Request, state, reason string) error {
	current, e := os.ReadFile(d.Executor.Files[0])
	if e != nil {
		return e
	}
	sum := sha256.Sum256(current)
	raw, _ := json.Marshal(operationsState{Digest: fmt.Sprintf("%x", sum), Enabled: r.Enabled, Generation: r.Generation, CheckedAt: time.Now().UTC(), State: state, Reason: reason})
	return atomicPrivate(filepath.Join(d.StateDirectory, r.Protocol+"-status.json"), raw)
}

func (d *HostDriver) opsWebPlan(ctx context.Context, raw []byte, r Request) (ComposePlan, error) {
	var doc map[string]json.RawMessage
	var model composeModel
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &model) != nil || model.Services["web"] == nil {
		return ComposePlan{}, errors.New("Nginx 服务定义不可读取")
	}
	source, e := nginxSource(raw)
	if e != nil {
		return ComposePlan{}, e
	}
	root, e := filepath.EvalSymlinks(d.Executor.Directory)
	if e != nil {
		return ComposePlan{}, e
	}
	source, e = filepath.EvalSymlinks(source)
	if e != nil {
		return ComposePlan{}, e
	}
	rel, e := filepath.Rel(root, source)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ComposePlan{}, errors.New("Nginx 主配置必须位于已接管安装目录")
	}
	nginx, e := os.ReadFile(source)
	if e != nil || len(nginx) > 1<<20 {
		return ComposePlan{}, errors.New("Nginx 主配置不可读取")
	}
	if !strings.Contains(string(nginx), "include /etc/nginx/access-server/*.conf;") || !strings.Contains(string(nginx), "include /etc/nginx/optional/*.conf;") || strings.Contains(string(nginx), "real_ip_header") || strings.Contains(string(nginx), "set_real_ip_from") {
		return ComposePlan{}, errors.New("仅支持标准边缘 Nginx 配置；自定义 real-ip 或入口策略需先解除冲突")
	}
	if r.Protocol == "web-access" && r.Enabled {
		// The address checked by Nginx must equal the authenticated API's source
		// address, not a client-controlled forwarding chain.
		for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "X-Client-IP"} {
			rule := regexp.MustCompile(`(?im)^\s*proxy_set_header\s+` + header + `\s+([^;]+);`)
			matches := rule.FindAllSubmatch(nginx, -1)
			if len(matches) != 1 || strings.TrimSpace(string(matches[0][1])) != "$remote_addr" {
				return ComposePlan{}, errors.New("入口限制需要标准 Nginx 来源头覆盖规则，当前配置无法证明管理员不会被误封")
			}
		}
	}
	var mounts []json.RawMessage
	if json.Unmarshal(model.Services["web"]["volumes"], &mounts) != nil {
		return ComposePlan{}, errors.New("Nginx 挂载不可读取")
	}
	target := "/etc/nginx/access-server/managed.conf"
	var content []byte
	if r.Protocol == "web-access" {
		content, e = accessPolicy(r)
	} else {
		target = "/etc/nginx/optional/agent-updates.conf"
		content = []byte(agentRepositoryNginx)
		if !r.Enabled {
			content = []byte("location ^~ /agent-updates/ { return 404; }\n")
		}
	}
	if e != nil {
		return ComposePlan{}, e
	}
	digest := sha256.Sum256(content)
	path := filepath.Join(d.StateDirectory, fmt.Sprintf("%s-%x.conf", r.Protocol, digest[:12]))
	out := []json.RawMessage{}
	for _, mount := range mounts {
		var m composeMount
		if json.Unmarshal(mount, &m) != nil {
			return ComposePlan{}, errors.New("invalid web mount")
		}
		conflicting := strings.HasPrefix(m.Target, "/etc/nginx/access-") && r.Protocol == "web-access" || m.Target == target || r.Protocol == "agent-repository" && m.Target == "/srv/agent-updates"
		if conflicting {
			rel, e := filepath.Rel(d.StateDirectory, m.Source)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || m.Type != "bind" || !m.ReadOnly {
				return ComposePlan{}, errors.New("存在未接管的 Nginx 策略或更新仓库挂载，拒绝覆盖")
			}
			continue
		}
		out = append(out, mount)
	}
	mount, _ := json.Marshal(composeMount{Type: "bind", Source: path, Target: target, ReadOnly: true})
	out = append(out, mount)
	if r.Protocol == "agent-repository" && r.Enabled {
		export, e := d.prepareRepository(ctx, r)
		if e != nil {
			return ComposePlan{}, e
		}
		mount, _ := json.Marshal(composeMount{Type: "bind", Source: export, Target: "/srv/agent-updates", ReadOnly: true})
		out = append(out, mount)
	}
	model.Services["web"]["volumes"], _ = json.Marshal(out)
	doc["services"], _ = json.Marshal(model.Services)
	document, e := json.Marshal(doc)
	return ComposePlan{Document: document, AuxiliaryPath: path, AuxiliaryContent: content}, e
}

func (d *HostDriver) verifyWebEntry(ctx context.Context) error {
	// The dedicated loopback recovery channel validates the application document,
	// not /healthz (which is deliberately exempt from access restrictions).
	_, e := d.Executor.run(ctx, 15*time.Second, "verify management recovery entry", "exec", "-T", "web", "wget", "-q", "--no-check-certificate", "-T", "5", "-O", "/dev/null", "https://127.0.0.1:8443/index.html")
	return e
}
