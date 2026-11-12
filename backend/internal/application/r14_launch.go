package application

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

var nativeLaunchAlias = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var nativeDNSLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func nativeLaunchHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !nativeDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// Only public connection metadata is returned. Each real SSH connection still
// authenticates its key/MFA and rechecks current resource authorization.
func (a *App) nativeSSHLaunch(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	var out map[string]any
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		asset, e := a.sessionAsset(ctx, u, id)
		if e != nil {
			return e
		}
		if asset.Protocol != "ssh" {
			return problem(409, "entry_unavailable", "此资产不支持原生 SSH")
		}
		if e = a.enforcePolicy(ctx, u, id, sourceIP(r), "connect", ""); e != nil {
			return e
		}
		if !usernamePattern.MatchString(u.Username) || !nativeLaunchAlias.MatchString(asset.Alias) {
			return problem(409, "asset_alias_required", "请管理员为资产设置 1–64 位字母、数字、点、下划线或短横线组成的唯一别名")
		}
		settings, e := a.identity.OAuthSettings(ctx)
		if e != nil {
			return e
		}
		if !settings.NativeEnabled || a.effectiveListenerAddress("ssh") == "" {
			return problem(409, "native_disabled", "管理员尚未开启原生客户端访问")
		}
		if !a.nativeSSHReady.Load() {
			return problem(503, "gateway_unavailable", "原生 SSH 网关暂不可用")
		}
		_, portText, e := net.SplitHostPort(a.effectiveListenerAddress("ssh"))
		port, pe := strconv.Atoi(portText)
		host := a.config.NativeSSHName
		if e != nil || pe != nil || port < 1 || port > 65535 || !nativeLaunchHost(host) {
			return problem(503, "gateway_address_invalid", "请管理员配置有效的原生 SSH 网关对外地址和端口")
		}
		public, e := a.effectiveGatewayPublicKey(ctx)
		if e != nil {
			return e
		}
		key, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(public))
		if e != nil || len(options) != 0 || len(rest) != 0 {
			return fault.Invalid
		}
		public = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		user := u.Username + ":" + asset.Alias
		name := "nt-" + id
		filename := "next-terminal-" + id + ".ssh_config"
		knownHost := host
		if port != 22 {
			knownHost = fmt.Sprintf("[%s]:%d", host, port)
		}
		config := fmt.Sprintf("Host %s\n    HostName %s\n    Port %d\n    User %s\n    StrictHostKeyChecking yes\n    UserKnownHostsFile ./next-terminal-known_hosts\n    GlobalKnownHostsFile none\n    PasswordAuthentication no\n    PreferredAuthentications publickey,keyboard-interactive\n    ForwardAgent no\n    ForwardX11 no\n    ClearAllForwardings yes\n", name, host, port, user)
		uri := "termark://connect?" + url.Values{"host": {host}, "port": {strconv.Itoa(port)}, "username": {user}}.Encode()
		out = map[string]any{"assetId": id, "assetName": asset.Name, "host": host, "port": port, "username": user, "fingerprint": ssh.FingerprintSHA256(key), "config": config, "configFilename": filename, "knownHosts": knownHost + " " + public + "\n", "knownHostsFilename": "next-terminal-known_hosts", "command": "ssh -F ./" + filename + " -i \"你的私钥路径\" " + name, "termarkUri": uri}
		configuration, e := a.system.SecurityConfiguration(ctx)
		if e != nil {
			return e
		}
		out["passwordAuthentication"] = !configuration.Values.Bool(system.NativePasswordDisabledKey) && !configuration.Values.Bool("disable-password-login")
		if out["passwordAuthentication"] == true {
			out["passwordCommand"] = "ssh -o PasswordAuthentication=yes -o PreferredAuthentications=password,keyboard-interactive -F ./" + filename + " " + name
		}
		forward, e := a.forwardSettings(ctx)
		if e != nil {
			return e
		}
		targets := []map[string]any{}
		allowed := forward.Enabled
		for _, action := range []string{"execute", "upload", "download"} {
			d, e := a.policyDecision(ctx, u, id, sourceIP(r), action, "")
			if e != nil {
				return e
			}
			allowed = allowed && d.Allowed && !d.Filtered
		}
		if allowed {
			for _, rule := range forward.Rules {
				if rule.AssetID == id {
					targets = append(targets, map[string]any{"host": rule.Host, "port": rule.Port})
				}
			}
		}
		out["forwardTargets"] = targets
		return nil
	})
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
	return nil
}
