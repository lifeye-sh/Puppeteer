package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
)

type agentInstallInput struct {
	AssetID     string                    `json:"assetId"`
	Name        string                    `json:"name"`
	Endpoint    string                    `json:"endpoint"`
	Development bool                      `json:"allowInsecureDevelopment"`
	Scope       connectivity.NetworkScope `json:"networkScope"`
}
type agentInstallation struct {
	AssetName string    `json:"assetName"`
	ID        string    `json:"id"`
	AssetID   string    `json:"assetId"`
	AgentID   string    `json:"agentId"`
	State     string    `json:"state"`
	Step      string    `json:"step"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

func (a *App) routesAgentInstall(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /api/admin/agent-installations/config", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		endpoint, revision, e := a.system.AgentEndpoint(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"endpoint": endpoint, "revision": revision})
		return nil
	})
	handle("GET /api/admin/agent-installations", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		// A worker has a three-minute lifetime. Retain interrupted jobs across restarts.
		_, e := a.db.Exec(r.Context(), `WITH expired AS (UPDATE agent_installations SET state='failed',message='安装任务中断或超时；请检查远端服务及目录后重试',updated_at=now() WHERE state='running' AND created_at<now()-interval '4 minutes' RETURNING agent_id) UPDATE agent_gateway_configs SET revoked_at=now() WHERE id IN (SELECT agent_id FROM expired)`)
		if e != nil {
			return e
		}
		rows, e := a.db.Rows(r.Context(), `SELECT j.id,j.asset_id,a.display_name,COALESCE(j.agent_id::text,''),j.state,j.step,j.message,j.created_at FROM agent_installations j JOIN resource_registry a ON a.id=j.asset_id ORDER BY j.created_at DESC LIMIT 100`)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []agentInstallation{}
		for rows.Next() {
			var v agentInstallation
			if e = rows.Scan(&v.ID, &v.AssetID, &v.AssetName, &v.AgentID, &v.State, &v.Step, &v.Message, &v.CreatedAt); e != nil {
				return e
			}
			out = append(out, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	handle("POST /api/admin/agent-installations", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in agentInstallInput
		if e = decode(w, r, &in); e != nil {
			return e
		}
		if in.Endpoint == "" {
			in.Endpoint, _, e = a.system.AgentEndpoint(r.Context())
			if e != nil {
				return e
			}
		}
		cfg := agent.Config{Endpoint: in.Endpoint, AllowInsecureDevelopment: in.Development}
		if _, e = cfg.URL(); e != nil {
			return problem(422, "invalid_endpoint", "请输入目标机器可访问的 HTTPS 后端地址；HTTP 仅可显式用于开发环境")
		}
		if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || len(in.Endpoint) > 2048 {
			return fault.Invalid
		}
		if e = in.Scope.Validate(); e != nil {
			return e
		}
		asset, e := a.sessionAsset(r.Context(), u, in.AssetID)
		if e != nil {
			return e
		}
		if asset.Protocol != "ssh" {
			return problem(409, "protocol_unsupported", "安装 Agent 需要 SSH 资产")
		}
		trust, e := a.connectivity.Get(r.Context(), asset.ID)
		if e != nil || trust.Host != asset.Host || trust.Port != asset.Port {
			return problem(409, "host_key_untrusted", "请先在资产页面核验并信任 SSH 主机密钥")
		}
		var id string
		e = a.mutate(r, "agent.install_requested", func(ctx context.Context, _ *identity.User) (string, error) {
			// Serialize the check across backend instances; at most four active installers.
			if _, err := a.db.Exec(ctx, `SELECT pg_advisory_xact_lock(16062026)`); err != nil {
				return "", err
			}
			var n int
			if err := a.db.Row(ctx, `SELECT count(*) FROM agent_installations WHERE state='running'`).Scan(&n); err != nil {
				return "", err
			}
			if n >= 4 {
				return "", problem(429, "installer_busy", "安装任务已满，请等待当前任务结束")
			}
			err := a.db.Row(ctx, `INSERT INTO agent_installations(asset_id,actor_id) VALUES($1,$2) RETURNING id`, asset.ID, u.ID).Scan(&id)
			return id, err
		})
		if e != nil {
			return e
		}
		// Clone the authenticated request for repeated authorization checks; never persist its cookie.
		request := r.Clone(context.Background())
		go a.installAgent(request, id, in)
		writeJSON(w, 202, map[string]string{"id": id})
		return nil
	})
}

func (a *App) installAgent(r *http.Request, id string, in agentInstallInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r = r.Clone(ctx)
	state, step, message, agentID := "failed", "connecting", "SSH 连接失败，请检查凭据、网络路径和主机信任", ""
	defer func() {
		end, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if state != "succeeded" && agentID != "" {
			_ = a.connectivity.RevokeDevice(end, agentID)
			a.connectivity.DisconnectAgent(agentID)
		}
		_ = a.db.Within(end, func(c context.Context) error {
			if _, e := a.db.Exec(c, `UPDATE agent_installations SET state=$2,step=$3,message=$4,updated_at=now() WHERE id=$1 AND state='running'`, id, state, step, message); e != nil {
				return e
			}
			var actor string
			if e := a.db.Row(c, `SELECT actor_id FROM agent_installations WHERE id=$1`, id).Scan(&actor); e != nil {
				return e
			}
			return a.audit.Detail(c, actor, "agent.install_"+state, id, sourceIP(r), map[string]string{"target": in.AssetID, "agentId": agentID, "step": step})
		})
	}()
	progress := func(s, m string) error {
		step, message = s, m
		_, e := a.db.Exec(ctx, `UPDATE agent_installations SET step=$2,message=$3,updated_at=now() WHERE id=$1 AND state='running'`, id, s, m)
		return e
	}
	u, e := a.require(r, "identity.manage")
	if e != nil {
		message = "安装授权已失效"
		return
	}
	asset, e := a.sessionAsset(ctx, u, in.AssetID)
	if e != nil {
		return
	}
	trust, e := a.connectivity.Get(ctx, asset.ID)
	if e != nil || trust.Host != asset.Host || trust.Port != asset.Port {
		return
	}
	var credentialVersion int64
	authorize := func(c context.Context) error {
		user, e := a.require(r.Clone(c), "identity.manage")
		if e != nil {
			return e
		}
		latest, e := a.sessionAsset(c, user, asset.ID)
		if e != nil {
			return e
		}
		key, e := a.connectivity.Get(c, asset.ID)
		if e != nil {
			return e
		}
		if latest.Version != asset.Version || key.Version != trust.Version {
			return fault.Denied
		}
		credential, e := a.vault.Get(c, asset.CredentialID)
		if e != nil {
			return e
		}
		if credentialVersion != 0 && credential.Version != credentialVersion {
			return fault.Denied
		}
		return nil
	}
	var auth ssh.AuthMethod
	var username string
	e = a.db.Within(ctx, func(c context.Context) error {
		cred, method, err := a.vault.InstallationAuth(c, asset.CredentialID, u.ID, id)
		auth = method
		username = cred.Username
		credentialVersion = cred.Version
		return err
	})
	if e != nil {
		return
	}
	var transport connectivity.Dialer
	switch {
	case asset.AgentID != "":
		transport = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, asset.AgentID, id, h, p, authorize)
		}
	case asset.JumpID != "":
		transport = a.jumpDialer(ctx, asset.JumpID, u.ID, id)
	case asset.GatewayGroupID != "":
		transport = a.groupDialer(ctx, asset.GatewayGroupID, u.ID, id, authorize)
	case asset.ProxyID != "":
		transport = a.proxyDialer(ctx, asset.ProxyID, u.ID, id)
	}
	dc, stop := context.WithTimeout(ctx, 15*time.Second)
	client, e := connectivity.ConnectUsing(dc, trust, username, auth, transport)
	stop()
	auth = nil
	if e != nil {
		return
	}
	defer client.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c, done := context.WithTimeout(ctx, time.Second)
				err := authorize(c)
				done()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	if e = progress("preflight", "检查 Linux amd64、systemd、安装目录和 root/免密 sudo 权限"); e != nil {
		return
	}
	output, e := installRun(client, installPreflight, nil)
	if e != nil {
		message = "预检失败：需要 Linux x86_64、运行中的 systemd、root 或免密 sudo，且安装目录/同名服务尚不存在"
		return
	}
	prefix := ""
	if strings.TrimSpace(output) == "sudo" {
		prefix = "sudo -n "
	} else if strings.TrimSpace(output) != "root" {
		message = "预检结果不符合预期"
		return
	}
	binary, e := os.Open(a.agentBinaryPath())
	if e != nil {
		message = "后端部署缺少 Linux amd64 Agent 程序"
		return
	}
	defer binary.Close()
	info, e := binary.Stat()
	if e != nil || info.Size() > 128*1024*1024 {
		message = "Agent 程序无效"
		return
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, binary); e != nil {
		return
	}
	if _, e = binary.Seek(0, 0); e != nil {
		return
	}
	if e = progress("registering", "创建受限设备身份"); e != nil {
		return
	}
	var enrolled connectivity.Enrolled
	e = a.db.Within(ctx, func(c context.Context) error {
		reg, err := a.connectivity.CreateRegistration(c, in.Name, u.ID, in.Scope)
		if err != nil {
			return err
		}
		enrolled, err = a.connectivity.Enroll(c, connectivity.Enrollment{RegistrationToken: reg.Token, Name: in.Name, OS: "linux", Arch: "amd64", Version: Release})
		if err != nil {
			return err
		}
		if _, err = a.db.Exec(c, `UPDATE agent_installations SET agent_id=$2 WHERE id=$1`, id, enrolled.AgentID); err != nil {
			return err
		}
		return a.audit.Detail(c, u.ID, "agent.enrolled_by_installer", id, sourceIP(r), map[string]string{"target": enrolled.AgentID, "assetId": asset.ID})
	})
	if e != nil {
		message = "创建设备身份失败"
		return
	}
	agentID = enrolled.AgentID
	cfg, _ := json.Marshal(agent.Config{SchemaVersion: 1, Endpoint: in.Endpoint, AgentID: agentID, DeviceKey: enrolled.DeviceKey, AllowInsecureDevelopment: in.Development, CollectionSeconds: 5})
	defer clear(cfg)
	enrolled.DeviceKey = ""
	if e = progress("installing", "通过加密 SSH 上传程序和私有配置，安装并启动 systemd 服务"); e != nil {
		return
	}
	staging := "/tmp/nt-agent-install-" + id
	script := fmt.Sprintf(installScript, staging, hex.EncodeToString(hash.Sum(nil)))
	reader, writer := io.Pipe()
	uploaded := make(chan error, 1)
	go func() {
		tw := tar.NewWriter(writer)
		err := writeInstallTar(tw, binary, info.Size(), cfg)
		if err == nil {
			err = tw.Close()
		}
		_ = writer.CloseWithError(err)
		uploaded <- err
	}()
	_, e = installRun(client, prefix+"sh -c "+shellQuote(script), reader)
	_ = reader.Close()
	uploadErr := <-uploaded
	if e != nil || uploadErr != nil {
		message = "上传或服务安装失败；设备凭据已撤销。请检查权限/磁盘及 /opt/next-terminal-agent、/var/lib/next-terminal-agent 中的残留，安装不会覆盖已有文件"
		return
	}
	if e = progress("waiting_online", "服务已启动，等待 Agent 与后端建立认证连接"); e != nil {
		return
	}
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			message = "安装超时或授权失效；请检查远端服务，设备凭据已撤销"
			return
		case <-timer.C:
			message = "服务已安装但 Agent 未上线，请检查后端地址、TLS 证书和出站网络；设备凭据已撤销，请清理失败安装后重试"
			return
		case <-tick.C:
			if a.connectivity.AgentDialReady(agentID) {
				state, step, message = "succeeded", "completed", "安装完成，Agent 已上线并可拨号"
				return
			}
		}
	}
}

func (a *App) agentBinaryPath() string {
	if a.config.AgentBinaryPath != "" {
		return a.config.AgentBinaryPath
	}
	return "/app/nt-agent-linux-amd64"
}

// Commands are fixed scripts. User-controlled values travel only in a private JSON file.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

type installOutput struct{ bytes.Buffer }

func (b *installOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 4096 {
		keep := 4096 - b.Len()
		if len(p) > keep {
			p = p[:keep]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func installRun(client *ssh.Client, command string, input io.Reader) (string, error) {
	s, e := client.NewSession()
	if e != nil {
		return "", e
	}
	defer s.Close()
	var out installOutput
	s.Stdout = &out
	s.Stderr = io.Discard
	s.Stdin = input
	e = s.Run(command)
	return out.String(), e
}
func writeInstallTar(tw *tar.Writer, binary io.Reader, size int64, cfg []byte) error {
	for _, f := range []struct {
		name string
		size int64
		mode int64
		data io.Reader
	}{{"nt-agent", size, 0755, binary}, {"agent.json", int64(len(cfg)), 0600, bytes.NewReader(cfg)}, {"nt-agent.service", int64(len(installUnit)), 0644, strings.NewReader(installUnit)}} {
		if e := tw.WriteHeader(&tar.Header{Name: f.name, Size: f.size, Mode: f.mode}); e != nil {
			return e
		}
		if _, e := io.CopyN(tw, f.data, f.size); e != nil {
			return e
		}
	}
	return nil
}

const installPreflight = `set -eu
test "$(uname -s)" = Linux
test "$(uname -m)" = x86_64
test -d /run/systemd/system
command -v systemctl >/dev/null
command -v tar >/dev/null
command -v sha256sum >/dev/null
if test "$(id -u)" = 0; then mode=root; else sudo -n true; mode=sudo; fi
if test "$mode" = root; then
 sh -c 'test ! -e /opt/next-terminal-agent && test ! -e /var/lib/next-terminal-agent && test ! -e /etc/systemd/system/nt-agent.service && ! systemctl cat nt-agent.service >/dev/null 2>&1 && command -v useradd >/dev/null'
else
 sudo -n sh -c 'test ! -e /opt/next-terminal-agent && test ! -e /var/lib/next-terminal-agent && test ! -e /etc/systemd/system/nt-agent.service && ! systemctl cat nt-agent.service >/dev/null 2>&1 && command -v useradd >/dev/null'
fi
printf '%s' "$mode"`
const installScript = `set -eu
umask 077
stage='%s'
mkdir -m 700 "$stage"
trap 'rm -f "$stage/nt-agent" "$stage/agent.json" "$stage/nt-agent.service"; rmdir "$stage"' EXIT
tar -xf - -C "$stage"
echo '%s  '"$stage/nt-agent" | sha256sum -c - >/dev/null
test ! -e /etc/systemd/system/nt-agent.service
! systemctl cat nt-agent.service >/dev/null 2>&1
mkdir -m 755 /opt/next-terminal-agent
mkdir -m 700 /var/lib/next-terminal-agent
if ! id nt-agent >/dev/null 2>&1; then useradd --system --no-create-home --home-dir /var/lib/next-terminal-agent --shell /bin/false nt-agent; fi
install -m 755 "$stage/nt-agent" /opt/next-terminal-agent/nt-agent
install -m 600 "$stage/agent.json" /var/lib/next-terminal-agent/agent.json
chown -R nt-agent /var/lib/next-terminal-agent
(set -C; cat "$stage/nt-agent.service" > /etc/systemd/system/nt-agent.service)
chmod 644 /etc/systemd/system/nt-agent.service
systemctl daemon-reload
systemctl enable --now nt-agent.service
systemctl is-active --quiet nt-agent.service
`
const installUnit = `[Unit]
Description=Puppeteer Agent
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=nt-agent
ExecStart=/opt/next-terminal-agent/nt-agent --config /var/lib/next-terminal-agent/agent.json
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/next-terminal-agent
UMask=0077
[Install]
WantedBy=multi-user.target
`
