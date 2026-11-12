package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"strings"
)

func (a *App) policyRP(ctx context.Context) string {
	p, e := a.passkeyProtocol(ctx)
	if e == nil {
		return p.RPID()
	}
	return ""
}
func (a *App) assertAdminLoginPath(ctx context.Context) error {
	if _, e := a.db.Exec(ctx, `SELECT pg_advisory_xact_lock(81723048)`); e != nil {
		return e
	}
	if e := a.identity.AssertAdminLoginPath(ctx, a.policyRP(ctx)); e != nil {
		if e == fault.Conflict {
			return problem(409, "last_admin_login_path", "此操作会移除最后一个已验证的管理员登录方式。请先开启本地密码登录，或用另一个管理员完成备用方式登录。")
		}
		return e
	}
	return nil
}
func (a *App) loginPolicy(w http.ResponseWriter, r *http.Request) error {
	u, e := a.require(r, "identity.manage")
	if e != nil {
		return e
	}
	if u.Type != "admin" {
		return fault.Denied
	}
	if r.Method == "GET" {
		v, e := a.identity.LoginPolicy(r.Context())
		if e != nil {
			return e
		}
		count, e := a.identity.AdminLoginAlternatives(r.Context(), u.ID, true, a.policyRP(r.Context()))
		if e != nil {
			return e
		}
		effective, e := a.identity.PasswordLoginEnabled(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"passwordEnabled": v.PasswordEnabled, "effectivePasswordEnabled": effective, "version": v.Version, "canDisable": count > 0})
		return nil
	}
	var in struct {
		backupProof
		identity.LoginPolicy
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	e = a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		if u.Type != "admin" {
			return fault.Denied
		}
		if !in.PasswordEnabled {
			count, e := a.identity.AdminLoginAlternatives(ctx, u.ID, true, a.policyRP(ctx))
			if e != nil {
				return e
			}
			if count == 0 {
				return problem(409, "alternate_login_required", "请在 15 分钟内用当前管理员的 LDAP、OIDC、企业微信、短信或 Passkey 完成一次登录及 MFA，再关闭本地密码登录")
			}
		}
		if e := a.identity.SaveLoginPolicy(ctx, in.LoginPolicy, u.ID); e != nil {
			return e
		}
		if e := a.assertAdminLoginPath(ctx); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.login_policy_changed", requestID(r), sourceIP(r), nil)
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) EnablePasswordLoginFromCLI(ctx context.Context, operator, reason string) error {
	operator = strings.TrimSpace(operator)
	reason = strings.TrimSpace(reason)
	if operator == "" || len(operator) > 128 || len(reason) < 5 || len(reason) > 512 {
		return fault.Invalid
	}
	return a.db.Within(ctx, func(ctx context.Context) error {
		if e := a.identity.EnablePasswordLogin(ctx); e != nil {
			return e
		}
		return a.audit.Detail(ctx, "", "identity.password_login_recovered", "cli", "local", map[string]string{"operator": operator, "reason": reason})
	})
}
