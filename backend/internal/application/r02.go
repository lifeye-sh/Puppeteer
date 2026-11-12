package application

import (
	"context"
	"errors"
	"net/http"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) allowed(ctx context.Context, u *identity.User, action string) error {
	actions, e := a.authz.Actions(ctx, u.ID, u.Type)
	if e != nil {
		return e
	}
	if !slices.Contains(actions, action) {
		return fault.Denied
	}
	return nil
}
func (a *App) require(r *http.Request, action string) (*identity.User, error) {
	u, e := a.authenticated(r)
	if e != nil {
		return nil, e
	}
	return u, a.allowed(r.Context(), u, action)
}
func (a *App) setSession(w http.ResponseWriter, r *http.Request, value string, secure ...bool) error {
	age, err := a.identity.CookieLifetime(r.Context(), value)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: a.config.SecureCookies || (len(secure) > 0 && secure[0]) || r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https://"), SameSite: http.SameSiteLaxMode, MaxAge: age})
	return nil
}
func (a *App) loginStatus(w http.ResponseWriter, r *http.Request) error {
	s, e := a.system.State(r.Context())
	if e != nil {
		return e
	}
	if !s.Initialized {
		return problem(418, "setup_required", "请先初始化管理员")
	}
	u, e := a.identity.Session(r.Context(), token(r))
	if e != nil {
		return e
	}
	status, phase := "Unlogged", ""
	if u != nil {
		phase = u.Phase
		switch phase {
		case "authenticated":
			status = "Logged In"
		case "mfa_pending":
			status = "OTP Required"
		case "mfa_enroll":
			status = "MFA Enrollment Required"
		case "password_change":
			status = "Password Change Required"
		}
	}
	passwordEnabled, e := a.identity.PasswordLoginEnabled(r.Context())
	if e != nil {
		return e
	}
	contactSettings, e := a.identity.ContactSettings(r.Context())
	if e != nil {
		return e
	}
	captchaEnabled, e := a.loginCaptchaEnabled(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"status": status, "phase": phase, "passwordEnabled": passwordEnabled, "captchaEnabled": captchaEnabled, "ldapEnabled": false, "smsEnabled": contactSettings.SMSLoginEnabled, "recoveryEnabled": contactSettings.RecoveryEnabled, "webauthnEnabled": a.passkeyEnabled(r.Context()), "wechatWorkEnabled": false, "oidcEnabled": false})
	return nil
}
func (a *App) accountInfo(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	actions, e := a.authz.Actions(r.Context(), u.ID, u.Type)
	if e != nil {
		return e
	}
	roles, e := a.authz.UserRoles(r.Context(), u.ID)
	if e != nil {
		return e
	}
	enabled, required, e := a.identity.MFAStatus(r.Context(), u.ID)
	if e != nil {
		return e
	}
	var localPassword bool
	if e = a.db.Row(r.Context(), `SELECT password_hash LIKE '$argon2id$%' FROM users WHERE id=$1`, u.ID).Scan(&localPassword); e != nil {
		return e
	}
	menus := []map[string]any{{"key": "backend-home", "checked": true}, {"key": "info", "checked": true}, {"key": "x-asset", "checked": true}}
	for action, menu := range map[string]string{"user.read": "user", "department.read": "department", "role.read": "role", "lock.read": "login-locked", "asset.read": "asset", "credential.read": "credential", "grant.read": "authorised-asset"} {
		if slices.Contains(actions, action) {
			menus = append(menus, map[string]any{"key": menu, "checked": true})
		}
	}
	writeJSON(w, 200, map[string]any{"id": u.ID, "username": u.Username, "nickname": u.Nickname, "type": u.Type, "enabledTotp": enabled, "mfaEnabled": enabled, "roles": roles, "permissions": []any{}, "actions": actions, "menus": menus, "language": "zh-CN", "forceTotpEnabled": required, "needChangePassword": false, "accessRequestEnabled": false, "apiKeyAccessEnabled": true, "dev": true, "passwordSet": localPassword})
	return nil
}
func (a *App) hashPermit(r *http.Request) (func(), error) {
	if e := a.identity.CheckIPLock(r.Context(), sourceIP(r)); e != nil {
		return nil, e
	}
	if !a.allowAttempt(sourceIP(r)) {
		return nil, problem(429, "rate_limited", "请求过多，请一分钟后重试")
	}
	select {
	case a.hashSlots <- struct{}{}:
		return func() { <-a.hashSlots }, nil
	default:
		return nil, problem(429, "busy", "请求过多，请稍后重试")
	}
}
func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	state, e := a.system.State(r.Context())
	if e != nil {
		return e
	}
	if !state.Initialized {
		return problem(418, "setup_required", "请先初始化管理员")
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Source   string `json:"source"`
		Key      string `json:"key"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if in.Source != "" && in.Source != "password" {
		return fault.Invalid
	}
	if len(in.Username) > 64 || len(in.Password) > 1024 {
		return fault.Invalid
	}
	in.Username = strings.ToLower(in.Username)
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	if e = a.checkLoginCaptcha(r, in.Key); e != nil {
		return e
	}
	var resultErr error
	var session, phase string
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if e := a.identity.CheckLock(ctx, in.Username, sourceIP(r)); e != nil {
			if errors.Is(e, fault.Locked) {
				resultErr = e
				return a.audit.Login(ctx, "", "locked", requestID(r), sourceIP(r))
			}
			return e
		}
		u, e := a.identity.Verify(ctx, in.Username, in.Password)
		if e != nil {
			return e
		}
		if u == nil {
			resultErr = problem(400, "invalid_credentials", "用户名或密码错误")
			if e = a.identity.FailedPassword(ctx, in.Username, sourceIP(r)); e != nil {
				return e
			}
			return a.audit.Login(ctx, "", "failed", requestID(r), sourceIP(r))
		}
		passwordEnabled, e := a.identity.PasswordLoginEnabled(ctx)
		if e != nil {
			return e
		}
		if !passwordEnabled {
			return problem(403, "password_login_disabled", "本地密码登录已关闭，请使用已配置的登录方式")
		}
		if e = a.identity.Unlock(ctx, in.Username); e != nil {
			return e
		}
		enabled, required, e := a.identity.MFAStatus(ctx, u.ID)
		if e != nil {
			return e
		}
		phase = "authenticated"
		outcome := "succeeded"
		if enabled {
			phase = "mfa_pending"
			outcome = "pending_mfa"
		} else if required {
			phase = "mfa_enroll"
			outcome = "pending_enrollment"
		}
		u.Phase = phase
		if e = a.identity.Revoke(ctx, token(r)); e != nil {
			return e
		}
		session, e = a.identity.Issue(ctx, *u, state.AuthEpoch, sourceIP(r))
		if e != nil {
			return e
		}
		return a.audit.Login(ctx, u.ID, outcome, requestID(r), sourceIP(r))
	})
	if e != nil {
		return e
	}
	if resultErr != nil {
		return resultErr
	}
	if err := a.setSession(w, r, session, strings.HasPrefix(r.Header.Get("Origin"), "https://")); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"token": "", "needTotp": phase == "mfa_pending", "needRebind": phase == "mfa_enroll"})
	return nil
}
func captchaBinding(r *http.Request) string {
	c, e := r.Cookie("nt_captcha")
	if e != nil {
		return ""
	}
	return c.Value
}
func (a *App) captcha(w http.ResponseWriter, r *http.Request) error {
	if !a.allowAttempt("captcha:" + sourceIP(r)) {
		return problem(429, "rate_limited", "验证码请求过多，请稍后重试")
	}
	binding := captchaBinding(r)
	if len(binding) != 64 {
		binding = identity.NewToken()
	}
	http.SetCookie(w, &http.Cookie{Name: "nt_captcha", Value: binding, Path: "/", HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	v, e := a.identity.Captcha(r.Context(), binding)
	if e != nil {
		return e
	}
	writeJSON(w, 200, v)
	return nil
}
func (a *App) captchaVerify(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Key     string `json:"key"`
		Purpose string `json:"purpose"`
		X       int    `json:"x"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Purpose != "login" || len(captchaBinding(r)) != 64 {
		return fault.Proof
	}
	key, e := a.identity.VerifyCaptcha(r.Context(), in.Key, captchaBinding(r), in.X)
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"key": key, "expiresIn": 120})
	return nil
}

// Match login lock order: login-name advisory lock, then user row, then session recheck.
func (a *App) securityTx(r *http.Request, fn func(context.Context, *identity.User) error) error {
	u, e := a.identity.Session(r.Context(), token(r))
	if e != nil {
		return e
	}
	if u == nil {
		return problem(401, "unauthenticated", "请重新登录")
	}
	return a.db.Within(r.Context(), func(ctx context.Context) error {
		if e := a.identity.CheckLock(ctx, u.Username); e != nil {
			return e
		}
		if e := a.identity.LockUser(ctx, u.ID); e != nil {
			return e
		}
		fresh, e := a.identity.Session(ctx, token(r))
		if e != nil {
			return e
		}
		if fresh == nil || fresh.Username != u.Username {
			return problem(401, "unauthenticated", "请重新登录")
		}
		if e := fn(ctx, fresh); e != nil {
			return e
		}
		return a.assertAdminLoginPath(ctx)
	})
}
func (a *App) verifyMFA(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TOTP     string `json:"totp"`
		Recovery string `json:"recoveryCode"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if (in.TOTP == "") == (in.Recovery == "") {
		return fault.Proof
	}
	var session, phase string
	var resultErr error
	var secure bool
	e := a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		secure = u.ExternalIdentityID != "" || u.LDAPIdentityID != "" || u.ContactID != "" || u.PasskeyID != ""
		if u.Phase != "mfa_pending" {
			return fault.Denied
		}
		var proof error
		phase = "authenticated"
		if in.Recovery != "" {
			proof = a.identity.ConsumeRecovery(ctx, u.ID, in.Recovery)
			phase = "mfa_enroll"
		} else {
			proof = a.identity.ConsumeTOTP(ctx, u.ID, in.TOTP)
		}
		if proof != nil {
			if !errors.Is(proof, fault.Proof) {
				return proof
			}
			resultErr = fault.Proof
			if e := a.identity.MFAFailure(ctx, token(r)); e != nil {
				return e
			}
			return a.audit.Login(ctx, u.ID, "mfa_failed", requestID(r), sourceIP(r))
		}
		if phase == "mfa_enroll" {
			if e := a.identity.RevokeAll(ctx, u.ID); e != nil {
				return e
			}
		} else if e := a.identity.Revoke(ctx, token(r)); e != nil {
			return e
		}
		state, e := a.system.State(ctx)
		if e != nil {
			return e
		}
		u.Phase = phase
		if phase == "authenticated" {
			now := time.Now()
			u.MFAVerifiedAt = &now
		}
		session, e = a.identity.Issue(ctx, *u, state.AuthEpoch, sourceIP(r))
		if e != nil {
			return e
		}
		outcome := "succeeded"
		if phase == "mfa_enroll" {
			outcome = "recovery_rebind_required"
		}
		return a.audit.Login(ctx, u.ID, outcome, requestID(r), sourceIP(r))
	})
	if e != nil {
		return e
	}
	if resultErr != nil {
		return resultErr
	}
	if err := a.setSession(w, r, session, secure); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"needRebind": phase == "mfa_enroll"})
	return nil
}
func (a *App) mfaStatus(w http.ResponseWriter, r *http.Request) error {
	u, e := a.identity.Session(r.Context(), token(r))
	if e != nil {
		return e
	}
	if u == nil {
		return problem(401, "unauthenticated", "请重新登录")
	}
	if u.Phase != "authenticated" && u.Phase != "mfa_enroll" {
		return fault.Denied
	}
	enabled, required, e := a.identity.MFAStatus(r.Context(), u.ID)
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "required": (!enabled && required) || u.Phase == "mfa_enroll", "phase": u.Phase, "passwordRequired": !externalMFAEnrollment(u)})
	return nil
}

// Only the short-lived enrollment phase can reuse a verified external primary
// login. Normal authenticated sessions still require explicit password proof.
func externalMFAEnrollment(u *identity.User) bool {
	return u != nil && u.Phase == "mfa_enroll" && (u.ExternalIdentityID != "" || u.LDAPIdentityID != "")
}

func (a *App) enrollMFA(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Password) > 1024 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var result map[string]string
	e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		if u.Phase != "authenticated" && u.Phase != "mfa_enroll" {
			return fault.Denied
		}
		if !externalMFAEnrollment(u) {
			verified, e := a.identity.Verify(ctx, u.Username, in.Password)
			if e != nil {
				return e
			}
			if verified == nil {
				return fault.Proof
			}
		}
		result, e = a.identity.BeginMFA(ctx, *u)
		return e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, result)
	return nil
}
func (a *App) confirmMFA(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TOTP string `json:"totp"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	var codes []string
	var session string
	var resultErr error
	var secure bool
	e := a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		secure = u.ExternalIdentityID != "" || u.LDAPIdentityID != "" || u.ContactID != "" || u.PasskeyID != ""
		if u.Phase != "authenticated" && u.Phase != "mfa_enroll" {
			return fault.Denied
		}
		var e error
		codes, e = a.identity.ConfirmMFA(ctx, u.ID, in.TOTP)
		if e != nil {
			if errors.Is(e, fault.Proof) {
				resultErr = e
				return a.identity.MFAFailure(ctx, token(r))
			}
			return e
		}
		if e = a.identity.RevokeAll(ctx, u.ID); e != nil {
			return e
		}
		state, e := a.system.State(ctx)
		if e != nil {
			return e
		}
		u.Phase = "authenticated"
		now := time.Now()
		u.MFAVerifiedAt = &now
		session, e = a.identity.Issue(ctx, *u, state.AuthEpoch, sourceIP(r))
		if e != nil {
			return e
		}
		return a.audit.Operation(ctx, u.ID, "mfa.enrolled", "succeeded", requestID(r), sourceIP(r))
	})
	if e != nil {
		return e
	}
	if resultErr != nil {
		return resultErr
	}
	if err := a.setSession(w, r, session, secure); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"recoveryCodes": codes})
	return nil
}
func validName(v string) bool { return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 100 }
func (a *App) mutate(r *http.Request, action string, fn func(context.Context, *identity.User) (string, error)) error {
	return a.db.Within(r.Context(), func(ctx context.Context) error {
		if e := a.identity.LockManagement(ctx); e != nil {
			return e
		}
		u, e := a.require(r.WithContext(ctx), "identity.manage")
		if e != nil {
			return e
		}
		target, e := fn(ctx, u)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, action, requestID(r), sourceIP(r), map[string]string{"target": target})
	})
}
func (a *App) saveUser(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	var in identity.ManagedUser
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	if !usernamePattern.MatchString(in.Username) || !validName(in.Nickname) || !slices.Contains([]string{"admin", "user"}, in.Type) || !slices.Contains([]string{"enabled", "disabled"}, in.Status) || len(in.RoleIDs) > 100 || len(in.DepartmentIDs) > 100 || len(in.Mail) > 254 || len(in.Remark) > 1000 {
		return fault.Invalid
	}
	if (in.ID == "" && in.Password == "") || len(in.Password) > 1024 {
		return fault.Invalid
	}
	hash := ""
	if in.Password != "" {
		if e := a.validateNewPassword(r.Context(), in.Username, in.Password); e != nil {
			return e
		}
		release, e := a.hashPermit(r)
		if e != nil {
			return e
		}
		defer release()
		hash = identity.HashPassword(in.Password)
	}
	var id string
	e := a.mutate(r, "user.saved", func(ctx context.Context, u *identity.User) (string, error) {
		if in.DefaultDepartmentSource != "" && u.Type != "admin" {
			return "", fault.Denied
		}
		if in.Password != "" {
			if e := a.validateNewPassword(ctx, in.Username, in.Password); e != nil {
				return "", e
			}
		}
		var e error
		id, e = a.identity.SaveUser(ctx, in, hash)
		if e != nil {
			return "", e
		}
		if in.DefaultDepartmentSource != "" {
			if e = a.audit.Detail(ctx, u.ID, "identity.federation_user_prepared", requestID(r), sourceIP(r), map[string]string{"userId": id, "source": in.DefaultDepartmentSource, "departmentId": in.DefaultDepartmentID}); e != nil {
				return "", e
			}
		}
		return id, a.authz.Assign(ctx, id, in.RoleIDs)
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) users(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "user.read"); e != nil {
		return e
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("current"))
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}
	if page > 1000000 {
		return fault.Invalid
	}
	items, total, e := a.identity.Users(r.Context(), r.URL.Query().Get("search"), (page-1)*size, size)
	if e != nil {
		return e
	}
	for i := range items {
		items[i].RoleIDs, e = a.authz.UserRoles(r.Context(), items[i].ID)
		if e != nil {
			return e
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
	return nil
}
func (a *App) resetMFA(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	var in struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
		Reason   string `json:"reason"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 5 || utf8.RuneCountInString(in.Reason) > 500 || len(in.Password) > 1024 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if e := a.identity.LockManagement(ctx); e != nil {
			return e
		}
		u, e := a.require(r.WithContext(ctx), "identity.manage")
		if e != nil {
			return e
		}
		v, e := a.identity.Verify(ctx, u.Username, in.Password)
		if e != nil {
			return e
		}
		if v == nil {
			return fault.Proof
		}
		enabled, _, e := a.identity.MFAStatus(ctx, u.ID)
		if e != nil {
			return e
		}
		if enabled {
			if e = a.identity.ConsumeTOTP(ctx, u.ID, in.TOTP); e != nil {
				return e
			}
		}
		if e = a.identity.ResetMFA(ctx, r.PathValue("id")); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "mfa.admin_reset", requestID(r), sourceIP(r), map[string]string{"target": r.PathValue("id"), "reason": in.Reason})
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) routesR02(handle func(string, func(http.ResponseWriter, *http.Request) error)) {
	handle("GET /api/captcha", a.captcha)
	handle("POST /api/captcha/verify", a.captchaVerify)
	handle("POST /api/validate-totp", a.verifyMFA)
	handle("GET /api/account/mfa", a.mfaStatus)
	handle("POST /api/account/mfa/enroll", a.enrollMFA)
	handle("POST /api/account/mfa/confirm", a.confirmMFA)
	handle("GET /api/admin/users/paging", a.users)
	handle("POST /api/admin/users", a.saveUser)
	handle("PUT /api/admin/users/{id}", a.saveUser)
	handle("DELETE /api/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "user.deleted", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			if e := a.identity.DeleteUser(ctx, id); e != nil {
				return "", e
			}
			return id, a.authz.Assign(ctx, id, nil)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("POST /api/admin/users/{id}/revoke-sessions", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "user.sessions_revoked", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			if e := a.identity.LockUser(ctx, id); e != nil {
				return "", e
			}
			return id, a.identity.RevokeAll(ctx, id)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("POST /api/admin/users/{id}/reset-mfa", a.resetMFA)
	handle("GET /api/admin/departments", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "department.read"); e != nil {
			return e
		}
		v, e := a.identity.Departments(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	saveDept := func(w http.ResponseWriter, r *http.Request) error {
		var in identity.Department
		if e := decode(w, r, &in); e != nil {
			return e
		}
		in.ID = r.PathValue("id")
		if !validName(in.Name) || in.Weight < 0 || in.Weight > 100000 {
			return fault.Invalid
		}
		var id string
		e := a.mutate(r, "department.saved", func(ctx context.Context, u *identity.User) (string, error) {
			var e error
			id, e = a.identity.SaveDepartment(ctx, in)
			return id, e
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	handle("POST /api/admin/departments", saveDept)
	handle("PUT /api/admin/departments/{id}", saveDept)
	handle("DELETE /api/admin/departments/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "department.deleted", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			return id, a.identity.DeleteDepartment(ctx, id)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/permissions", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "role.read"); e != nil {
			return e
		}
		v, e := a.authz.Catalog(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("GET /api/admin/roles", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "role.read"); e != nil {
			return e
		}
		v, e := a.authz.Roles(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	saveRole := func(w http.ResponseWriter, r *http.Request) error {
		var in authorization.Role
		if e := decode(w, r, &in); e != nil {
			return e
		}
		in.ID = r.PathValue("id")
		if !validName(in.Name) || len(in.Permissions) > 100 {
			return fault.Invalid
		}
		var id string
		e := a.mutate(r, "role.saved", func(ctx context.Context, u *identity.User) (string, error) {
			var e error
			id, e = a.authz.Save(ctx, in)
			return id, e
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	}
	handle("POST /api/admin/roles", saveRole)
	handle("PUT /api/admin/roles/{id}", saveRole)
	handle("DELETE /api/admin/roles/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "role.deleted", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			if e := a.authz.RemovePermissions(ctx, id); e != nil {
				return "", e
			}
			return id, a.authz.Delete(ctx, id)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	handle("GET /api/admin/login-locked", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "lock.read"); e != nil {
			return e
		}
		v, e := a.identity.Locks(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("DELETE /api/admin/login-locked/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.mutate(r, "login.unlocked", func(ctx context.Context, u *identity.User) (string, error) {
			id := r.PathValue("id")
			return id, a.identity.Unlock(ctx, id)
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
