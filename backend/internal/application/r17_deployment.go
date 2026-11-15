package application

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/fault"
	"os"
	"strings"
)

func (a *App) registerDeployment(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, operation := range []string{"check", "site-apply", "verify"} {
		h("POST /api/internal/deployment-"+operation, func(w http.ResponseWriter, r *http.Request) error {
			if !internalDeploymentRequest(r) {
				return fault.Denied
			}
			token, err := os.ReadFile(a.config.DeployTokenFile)
			if err != nil {
				return fault.Denied
			}
			expected := sha256.Sum256([]byte(strings.TrimSpace(string(token))))
			supplied := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
			if len(strings.TrimSpace(string(token))) < 32 || subtle.ConstantTimeCompare(expected[:], supplied[:]) != 1 {
				return fault.Denied
			}
			var req deploytask.Request
			if err = decode(w, r, &req); err != nil {
				return err
			}
			var actor identity.User
			if err = a.db.Row(r.Context(), `SELECT id,type FROM users WHERE id=$1 AND status='enabled' AND deleted_at IS NULL`, req.Actor).Scan(&actor.ID, &actor.Type); err != nil || actor.Type != "admin" {
				return fault.Denied
			}
			if err = a.allowed(r.Context(), &actor, "identity.manage"); err != nil {
				return err
			}
			if operation == "site-apply" {
				if req.Protocol != "website" || req.SiteID == "" {
					return fault.Invalid
				}
				err = a.ApplyDeploymentWebsite(r.Context(), req)
			} else {
				err = a.DeploymentApplicationCheck(r.Context(), req)
			}
			if err == nil && operation == "verify" {
				if req.Protocol != "website" {
					return fault.Invalid
				}
				a.configurationMu.Lock()
				listening := false
				if a.publicListeners != nil {
					for _, state := range a.publicListeners.States() {
						if state.Name == "website" {
							listening = true
						}
					}
				}
				a.configurationMu.Unlock()
				if listening != req.Enabled {
					err = errors.New("网站实际监听状态与部署期望不一致")
				}
			}
			message := ""
			if err != nil {
				message = err.Error()
			}
			writeJSON(w, 200, map[string]any{"passed": err == nil, "error": message})
			return nil
		})
	}
	call := func(w http.ResponseWriter, r *http.Request, path string, input any) error {
		token, err := os.ReadFile(a.config.DeployTokenFile)
		if err != nil || a.config.DeploySocket == "" {
			return problem(503, "deployment_not_enrolled", "宿主机部署执行器尚未接入，请完成支持自动部署的版本升级")
		}
		status, body, err := (deploytask.Client{Socket: a.config.DeploySocket, Token: strings.TrimSpace(string(token))}).Call(r.Context(), r.Method, path, input)
		if err != nil {
			return problem(503, "deployment_unavailable", err.Error())
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, status, json.RawMessage(body))
		return nil
	}
	admin := func(r *http.Request) error {
		u, err := a.require(r, "identity.manage")
		if err != nil {
			return err
		}
		if u.Type != "admin" {
			return fault.Denied
		}
		return nil
	}
	h("GET /api/admin/deployment/status", func(w http.ResponseWriter, r *http.Request) error {
		if err := admin(r); err != nil {
			return err
		}
		return call(w, r, "/v1/status", nil)
	})
	for _, action := range []string{"preflight", "tasks"} {
		h("POST /api/admin/deployment/"+action, func(w http.ResponseWriter, r *http.Request) error {
			if err := admin(r); err != nil {
				return err
			}
			var body struct {
				backupProof
				Request deploytask.Request `json:"request"`
			}
			if err := decode(w, r, &body); err != nil {
				return err
			}
			actor, actorErr := a.require(r, "identity.manage")
			if actorErr != nil {
				return actorErr
			}
			body.Request.Actor = actor.ID
			if body.Request.Protocol == "web-access" {
				body.Request.AdminIP = sourceIP(r)
				trusted, _ := r.Context().Value(trustedIPAppliedKey{}).(bool)
				if body.Request.Enabled && !trusted {
					return problem(422, "entry_client_ip_unverified", "尚未通过可信 Nginx 提取管理员来源 IP；请先配置客户端 IP 的可信代理范围，再启用入口限制")
				}
			}
			if body.Request.SiteID != "" {
				actor, err := a.require(r, "identity.manage")
				if err != nil {
					return err
				}
				body.Request.Actor = actor.ID
				sites, err := a.websites().List(r.Context())
				if err != nil {
					return err
				}
				enabled := false
				for _, site := range sites {
					if site.ID == body.Request.SiteID {
						enabled = enabled || body.Request.SiteEnabled
					} else {
						enabled = enabled || site.Enabled
					}
				}
				body.Request.Enabled = enabled
			}
			if err := deploytask.Validate(body.Request); err != nil {
				return problem(422, "deployment_invalid", err.Error())
			}
			if err := a.DeploymentApplicationCheck(r.Context(), body.Request); err != nil {
				return problem(422, "deployment_application_preflight", err.Error())
			}
			if action == "tasks" {
				if err := a.withPasswordProof(r, body.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
					if u.Type != "admin" {
						return fault.Denied
					}
					return a.audit.Detail(ctx, u.ID, "deployment.request", requestID(r), sourceIP(r), map[string]string{"protocol": body.Request.Protocol})
				}); err != nil {
					return err
				}
			}
			return call(w, r, "/v1/"+action, body.Request)
		})
	}
}

func internalDeploymentRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback() && r.Method == "POST" && (r.URL.Path == "/api/internal/deployment-check" || r.URL.Path == "/api/internal/deployment-site-apply" || r.URL.Path == "/api/internal/deployment-verify") && r.Header.Get("Origin") == "" && token(r) == "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// Website metadata must not change beneath an accepted host deployment. The final
// versioned database update additionally catches edits racing task acceptance.
func (a *App) websiteDeploymentIdle(ctx context.Context) error {
	if a.config.DeploySocket == "" {
		return nil
	}
	token, err := os.ReadFile(a.config.DeployTokenFile)
	if err != nil {
		return problem(503, "deployment_unavailable", "无法确认部署状态，请等待执行器恢复后重试")
	}
	status, raw, err := (deploytask.Client{Socket: a.config.DeploySocket, Token: strings.TrimSpace(string(token))}).Call(ctx, "GET", "/v1/status", nil)
	if err != nil || status != 200 {
		return problem(503, "deployment_unavailable", "无法确认部署状态，请等待执行器恢复后重试")
	}
	var view struct {
		Task *deploytask.Task `json:"task"`
	}
	if json.Unmarshal(raw, &view) != nil {
		return problem(503, "deployment_unavailable", "部署状态不可解析")
	}
	if view.Task != nil && view.Task.State == "running" && view.Task.Request.Protocol == "website" {
		return problem(409, "website_deployment_busy", "网站部署正在执行，请等待结束后再编辑或删除网站")
	}
	return nil
}
