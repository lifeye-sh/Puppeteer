package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) validateNewPassword(ctx context.Context, username, password string) error {
	if a.db.InTransaction(ctx) {
		var revision int64
		if err := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&revision); err != nil {
			return err
		}
	}
	configuration, err := a.system.SecurityConfiguration(ctx)
	if err != nil {
		return err
	}
	if err = system.ValidateNewPassword(configuration.Values, username, password); err != nil {
		return problem(422, "password_policy", err.Error())
	}
	return nil
}

func (a *App) accountPassword(w http.ResponseWriter, r *http.Request) error {
	u, err := a.identity.Session(r.Context(), token(r))
	if err != nil {
		return err
	}
	if u == nil {
		return problem(401, "unauthenticated", "请重新登录")
	}
	if u.Phase != "authenticated" && u.Phase != "password_change" {
		return fault.Denied
	}
	if r.Method == "GET" {
		configuration, err := a.system.SecurityConfiguration(r.Context())
		if err != nil {
			return err
		}
		enabled, _, err := a.identity.MFAStatus(r.Context(), u.ID)
		if err != nil {
			return err
		}
		var local bool
		if err = a.db.Row(r.Context(), `SELECT password_hash LIKE '$argon2id$%' FROM users WHERE id=$1`, u.ID).Scan(&local); err != nil {
			return err
		}
		policy, err := system.ParsePasswordPolicy(configuration.Values.String("password-strength-policy"))
		if err != nil {
			return err
		}
		if configuration.Values.String("password-strength-type") == "recommend" {
			policy = system.PasswordPolicy{MinLength: 12, MinCharacterType: 1, MustNotContainUsername: true, MustNotWeak: true}
		}
		writeJSON(w, 200, map[string]any{"localPassword": local, "expired": u.Phase == "password_change", "mfaRequired": enabled, "policy": policy, "expirationDays": configuration.Values.Int("password-expiration-period")})
		return nil
	}
	var in struct {
		Password    string `json:"password"`
		TOTP        string `json:"totp"`
		NewPassword string `json:"newPassword"`
		Confirm     string `json:"confirm"`
	}
	if err = decode(w, r, &in); err != nil {
		return err
	}
	if in.NewPassword != in.Confirm || in.NewPassword == in.Password || len(in.NewPassword) > 1024 {
		return problem(422, "password_change_invalid", "新密码须与确认一致，且不能与原密码相同")
	}
	if err = a.validateNewPassword(r.Context(), u.Username, in.NewPassword); err != nil {
		return err
	}
	err = a.withPasswordProofPhases(r, backupProof{Password: in.Password, TOTP: in.TOTP}, "", []string{"authenticated", "password_change"}, func(ctx context.Context, u *identity.User) error {
		if e := a.validateNewPassword(ctx, u.Username, in.NewPassword); e != nil {
			return e
		}
		if e := a.identity.ChangePassword(ctx, u.ID, identity.HashPassword(in.NewPassword)); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.password_changed", requestID(r), sourceIP(r), nil)
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
	return nil
}
