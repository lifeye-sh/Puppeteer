package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net"
	"net/url"
	"puppeteer/backend/internal/modules/connectivity"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/modules/pki"
	"puppeteer/backend/internal/modules/website"
	"puppeteer/backend/internal/platform/fault"
	"strconv"

	"puppeteer/backend/internal/platform/deploytask"

	"time"
)

func deploymentCertificate(leaf *x509.Certificate, host string, now time.Time) error {
	if leaf == nil {
		return fmt.Errorf("服务器证书不可解析")
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return fmt.Errorf("服务器证书尚未生效或已过期")
	}
	if leaf.VerifyHostname(host) != nil {
		return fmt.Errorf("服务器证书未覆盖访问域名 %s", host)
	}
	return nil
}

// DeploymentApplicationCheck is read-only. Host orchestration must separately
// verify socket binding, network policy and post-apply listener readiness.
func (a *App) DeploymentApplicationCheck(ctx context.Context, r deploytask.Request) error {
	if err := deploytask.Validate(r); err != nil {
		return err
	}
	if r.Protocol == "resources" {
		var busy bool
		if err := a.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE state='running' AND kind IN ('recording.mp4','backup.create'))`).Scan(&busy); err != nil {
			return errors.New("无法检查后台重任务状态")
		}
		if busy || a.maintenance.Load() {
			return errors.New("备份、转换或维护任务仍在执行，请完成后再切换资源档位")
		}
		return nil
	}
	if !r.Enabled && r.SiteID == "" {
		return nil
	}
	revision, err := a.system.Configuration(ctx, 0)
	if err != nil {
		return err
	}
	if r.Protocol == "mysql" || r.Protocol == "postgres" {
		id := revision.Values.String("db-proxy-certificate-id")
		if id == "" {
			if err = a.db.Row(ctx, `SELECT typed_value#>>'{}' FROM settings WHERE key='database-proxy-certificate-id'`).Scan(&id); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("数据库代理证书状态不可读取")
				}
				_, key, generationErr := pki.Generate("Database proxy preflight", []string{a.config.DatabaseProxyName}, 365, false, nil)
				clear(key)
				if generationErr != nil {
					return fmt.Errorf("无法为数据库代理访问域名生成证书")
				}
				return nil
			}
		}
		metadata, err := a.certificates().Get(ctx, id)
		if err != nil {
			return fmt.Errorf("数据库代理证书不存在或不可读取")
		}
		if metadata.Kind != "server" || metadata.RevokedAt != nil {
			return fmt.Errorf("数据库代理证书类型无效或已吊销")
		}
		pair, err := a.certificates().TLS(ctx, id)
		if err != nil {
			return fmt.Errorf("数据库代理证书与私钥不可用")
		}
		return deploymentCertificate(pair.Leaf, a.config.DatabaseProxyName, time.Now())
	}
	if r.Protocol == "website" {
		sites, err := a.websites().List(ctx)
		if err != nil {
			return err
		}
		if r.SiteID != "" {
			found, enabled := false, 0
			for i := range sites {
				if sites[i].ID == r.SiteID {
					found = true
					if sites[i].Version != r.SiteVersion && !(r.SiteEnabled && sites[i].Enabled && sites[i].Version == r.SiteVersion+1) && (r.SiteEnabled || sites[i].Enabled) {
						return fmt.Errorf("网站配置已变化，请刷新后重试")
					}
					sites[i].Enabled = r.SiteEnabled
				}
				if sites[i].Enabled {
					enabled++
				}
			}
			if !found {
				return fmt.Errorf("网站不存在")
			}
			if r.Enabled != (enabled > 0) {
				return fmt.Errorf("其他网站启停状态已变化，请重新预检")
			}
		}
		if r.IsDisable() {
			return nil
		} // Unrelated certificate failures must not prevent closing an entry.
		settings, err := a.websites().Settings(ctx)
		if err != nil {
			return err
		}
		for _, site := range sites {
			if !site.Enabled {
				continue
			}
			id := site.CertificateID
			if id == "" {
				id = settings.DefaultCertificateID
			}
			metadata, err := a.certificates().Get(ctx, id)
			if err != nil || metadata.Kind != "server" || metadata.RevokedAt != nil {
				return fmt.Errorf("网站 %s 的服务器证书不可用", site.Domain)
			}
			pair, err := a.certificates().TLS(ctx, id)
			if err != nil {
				return fmt.Errorf("网站 %s 的证书与私钥不可用", site.Domain)
			}
			if err = deploymentCertificate(pair.Leaf, site.Domain, time.Now()); err != nil {
				return err
			}
			if r.SiteID == "" || (site.ID == r.SiteID && r.SiteEnabled) {
				if err = a.probeWebsiteRoute(ctx, site, r.Actor); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ApplyDeploymentWebsite runs only through the authenticated host task executor.
func (a *App) ApplyDeploymentWebsite(ctx context.Context, r deploytask.Request) error {
	if r.SiteID == "" {
		return nil
	}
	if err := a.DeploymentApplicationCheck(ctx, r); err != nil {
		return err
	}
	return a.db.Within(ctx, func(ctx context.Context) error {
		current, err := a.websites().Get(ctx, r.SiteID)
		if err != nil {
			return err
		}
		if (!r.SiteEnabled && !current.Enabled) || (r.SiteEnabled && current.Enabled && current.Version == r.SiteVersion+1) {
			return nil
		}
		var actor string
		if err = a.db.Row(ctx, `SELECT id::text FROM users WHERE id=$1 AND type='admin' AND deleted_at IS NULL`, r.Actor).Scan(&actor); err != nil {
			return fmt.Errorf("部署操作管理员身份不可用")
		}
		tag, err := a.db.Exec(ctx, `UPDATE resource_registry SET enabled=$3,version=version+1 WHERE id=$1 AND version=$2 AND kind='website' AND deleted_at IS NULL`, r.SiteID, r.SiteVersion, r.SiteEnabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("网站版本冲突")
		}
		return a.audit.Detail(ctx, actor, "website.deployment", "deployd", "local", map[string]string{"id": r.SiteID})
	})
}

func probeWebsiteUpstream(ctx context.Context, site website.Website) error {
	if site.AgentID != "" || site.JumpID != "" || site.GatewayGroupID != "" || site.ProxyID != "" {
		return fmt.Errorf("网站间接连接必须通过平台路由预检")
	}
	return probeWebsiteDial(ctx, site, connectivity.DialTCP)
}

func (a *App) probeWebsiteRoute(ctx context.Context, site website.Website, actor string) error {
	if site.AgentID == "" && site.JumpID == "" && site.GatewayGroupID == "" && site.ProxyID == "" {
		return probeWebsiteUpstream(ctx, site)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	authorize := func(c context.Context) error {
		var valid bool
		if err := a.db.Row(c, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND type='admin' AND status='enabled' AND deleted_at IS NULL)`, actor).Scan(&valid); err != nil || !valid {
			return fault.Denied
		}
		err := a.allowed(c, &identity.User{ID: actor, Type: "admin"}, "identity.manage")
		if err != nil {
			return fault.Denied
		}
		current, err := a.websites().Get(c, site.ID)
		if err != nil || current.Version != site.Version {
			return fault.Conflict
		}
		return nil
	}
	if err := authorize(ctx); err != nil {
		return fmt.Errorf("网站预检授权失效或资源已变化")
	}
	lease := "website-preflight-" + identity.NewToken()[:16]
	var dial connectivity.Dialer
	switch {
	case site.AgentID != "":
		dial = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, site.AgentID, lease, h, p, authorize)
		}
	case site.JumpID != "":
		dial = a.jumpDialer(ctx, site.JumpID, actor, lease)
	case site.GatewayGroupID != "":
		dial = a.groupDialer(ctx, site.GatewayGroupID, actor, lease, authorize)
	case site.ProxyID != "":
		dial = a.proxyDialer(ctx, site.ProxyID, actor, lease)
	}
	if err := probeWebsiteDial(ctx, site, dial); err != nil {
		return err
	}
	if err := authorize(ctx); err != nil {
		return fmt.Errorf("网站预检期间授权或资源发生变化")
	}
	return nil
}

func probeWebsiteDial(ctx context.Context, site website.Website, dial connectivity.Dialer) error {
	target, err := url.Parse(site.Upstream)
	if err != nil || target.Hostname() == "" {
		return fmt.Errorf("网站上游地址无效")
	}
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "https" {
			port = "443"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("网站上游端口无效")
	}
	conn, err := dial(ctx, target.Hostname(), number)
	if err != nil {
		return fmt.Errorf("网站 %s 的上游端口不可达", site.Domain)
	}
	defer conn.Close()
	if target.Scheme == "https" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if site.UpstreamCA != "" && !roots.AppendCertsFromPEM([]byte(site.UpstreamCA)) {
			return fmt.Errorf("网站上游 CA 无效")
		}
		secured := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.Hostname(), RootCAs: roots})
		defer secured.Close()
		if err = secured.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("网站 %s 上游 TLS 验证失败", site.Domain)
		}
	}
	return nil
}
