package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) registerAccessMFA(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, prefix := range []string{"/api/account/access-mfa", "/api/native/v1/access-mfa"} {
		native := prefix == "/api/native/v1/access-mfa"
		for _, method := range []string{"GET", "POST"} {
			h(method+" "+prefix, func(w http.ResponseWriter, r *http.Request) error { return a.accessMFA(w, r, native) })
		}
	}
}
func (a *App) accessMFA(w http.ResponseWriter, r *http.Request, native bool) error {
	var input struct {
		TOTP string `json:"totp"`
	}
	if r.Method == "POST" {
		if err := decode(w, r, &input); err != nil {
			return err
		}
		if len(input.TOTP) != 6 {
			return fault.Invalid
		}
	}
	var out session.AccessMFAStatus
	err := a.db.Within(r.Context(), func(ctx context.Context) error {
		var user *identity.User
		var auth string
		if native {
			bearer, err := nativeBearer(r)
			if err != nil {
				return err
			}
			user, err = a.identity.NativeUser(ctx, bearer, "sessions:connect")
			if err != nil {
				return err
			}
			family, _, err := a.identity.OAuthToken(ctx, bearer, "access", identity.NativeClientUUID, false)
			if err != nil {
				return err
			}
			auth = identity.Digest(family.ID)
		} else {
			var err error
			user, err = a.authenticated(r.WithContext(ctx))
			if err != nil {
				return err
			}
			auth = identity.Digest(token(r))
		}
		if err := a.identity.LockUser(ctx, user.ID); err != nil {
			return err
		}
		var err error
		out, err = a.sessions.AccessMFAStatus(ctx, user.ID, auth)
		if err != nil {
			return err
		}
		if r.Method == "POST" {
			if !a.allowAttempt("access-mfa:" + user.ID) {
				return problem(429, "rate_limited", "二次验证过于频繁，请稍后重试")
			}
			if !out.Enrolled {
				return problem(409, "access_mfa_enrollment_required", "请先在个人账号中绑定动态口令")
			}
			if err = a.identity.ConsumeTOTP(ctx, user.ID, input.TOTP); err != nil {
				return err
			}
			if err = a.identity.RecordAccessMFA(ctx, user.ID, auth); err != nil {
				return err
			}
			if err = a.audit.Detail(ctx, user.ID, "session.access_mfa_verified", requestID(r), sourceIP(r), map[string]string{"native": strconv.FormatBool(native)}); err != nil {
				return err
			}
			out, err = a.sessions.AccessMFAStatus(ctx, user.ID, auth)
		}
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, out)
	return nil
}
