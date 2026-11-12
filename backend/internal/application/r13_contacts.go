package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

const contactCookie = "__Host-nt_contact"

func (a *App) contactSettings(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	if r.Method == "GET" {
		v, e := a.identity.ContactSettings(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	}
	var in struct {
		backupProof
		identity.ContactSettings
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	for _, id := range []string{in.EmailChannel, in.SMSChannel} {
		if id != "" && !resource.ValidID(id) {
			return fault.Invalid
		}
	}
	e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		if e := a.identity.SaveContactSettings(ctx, in.ContactSettings); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.contact_settings", requestID(r), sourceIP(r), map[string]string{})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) contacts(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	if r.Method == "GET" {
		items, e := a.identity.Contacts(r.Context(), u.ID)
		if e != nil {
			return e
		}
		settings, e := a.identity.ContactSettings(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items, "emailEnabled": settings.EmailChannel != "", "phoneEnabled": settings.SMSChannel != ""})
		return nil
	}
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	var proof backupProof
	if e = decode(w, r, &proof); e != nil {
		return e
	}
	e = a.withPasswordProof(r, proof, "", func(ctx context.Context, current *identity.User) error {
		if e := a.identity.RemoveContact(ctx, current.ID, r.PathValue("id")); e != nil {
			return e
		}
		return a.audit.Detail(ctx, current.ID, "identity.contact_removed", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) startContact(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		Kind    string `json:"kind"`
		Address string `json:"address"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	var challenge string
	e := a.withPasswordProof(r, in.backupProof, "", func(ctx context.Context, u *identity.User) error {
		var e error
		challenge, e = a.identity.StartContactChallenge(ctx, u.ID, token(r), in.Kind, in.Address, "contact")
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.contact_verification_started", requestID(r), sourceIP(r), map[string]string{"kind": in.Kind})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"challenge": challenge, "expiresIn": 300})
	return nil
}
func (a *App) finishContact(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.authenticated(r); e != nil {
		return e
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var in struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	c, e := a.identity.ConsumeContactChallenge(r.Context(), in.Challenge, token(r), "contact", in.Code)
	if e != nil {
		return e
	}
	e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		if u.ID != c.UserID || u.Phase != "authenticated" {
			return fault.Proof
		}
		if e := a.identity.SaveVerifiedContact(ctx, c); e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.contact_verified", requestID(r), sourceIP(r), map[string]string{"kind": c.Kind})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func contactBinding(r *http.Request) string {
	c, e := r.Cookie(contactCookie)
	if e != nil || len(c.Value) != 64 {
		return ""
	}
	return c.Value
}
func (a *App) startContactLogin(w http.ResponseWriter, r *http.Request) error {
	if !strings.HasPrefix(r.Header.Get("Origin"), "https://") {
		return fault.Proof
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var in struct {
		Kind    string `json:"kind"`
		Address string `json:"address"`
		Purpose string `json:"purpose"`
		Key     string `json:"key"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if in.Purpose != "recovery" && in.Purpose != "sms-login" {
		return fault.Invalid
	}
	address, e := identity.NormalizeContact(in.Kind, in.Address)
	if e != nil {
		return e
	}
	if e = a.checkLoginCaptcha(r, in.Key); e != nil {
		return e
	}
	binding := contactBinding(r)
	if binding == "" {
		binding = identity.NewToken()
	}
	challenge := identity.NewToken()
	user, lookupErr := a.identity.ContactUser(r.Context(), in.Kind, address)
	if lookupErr == nil {
		_ = a.db.Within(r.Context(), func(ctx context.Context) error {
			value, e := a.identity.StartContactChallenge(ctx, user, binding, in.Kind, address, in.Purpose)
			if e != nil {
				return e
			}
			challenge = value
			return a.audit.Detail(ctx, user, "identity.contact_challenge_requested", requestID(r), sourceIP(r), map[string]string{"purpose": in.Purpose})
		})
	}
	http.SetCookie(w, &http.Cookie{Name: contactCookie, Value: binding, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 300})
	writeJSON(w, 200, map[string]any{"challenge": challenge, "expiresIn": 300, "message": "若联系方式已验证且功能已启用，将发送验证码；请稍后查收。"})
	return nil
}
func (a *App) finishContactLogin(w http.ResponseWriter, r *http.Request) error {
	if !strings.HasPrefix(r.Header.Get("Origin"), "https://") {
		return fault.Proof
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var in struct {
		Challenge   string `json:"challenge"`
		Code        string `json:"code"`
		Purpose     string `json:"purpose"`
		NewPassword string `json:"newPassword"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if in.Purpose != "recovery" && in.Purpose != "sms-login" {
		return fault.Invalid
	}
	if in.Purpose == "recovery" && (len(in.NewPassword) < 8 || len(in.NewPassword) > 1024) {
		return fault.Invalid
	}
	c, e := a.identity.ConsumeContactChallenge(r.Context(), in.Challenge, contactBinding(r), in.Purpose, in.Code)
	if e != nil {
		return e
	}
	hash := ""
	if in.Purpose == "recovery" {
		hash = identity.HashPassword(in.NewPassword)
	}
	var session, phase string
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if in.Purpose == "recovery" {
			var username string
			if e := a.db.Row(ctx, `SELECT username FROM users WHERE id=$1`, c.UserID).Scan(&username); e != nil {
				return e
			}
			if e := a.validateNewPassword(ctx, username, in.NewPassword); e != nil {
				return e
			}
			if e := a.identity.RecoverContactPassword(ctx, c, hash); e != nil {
				return e
			}
			if e := a.audit.Detail(ctx, c.UserID, "identity.password_recovered", requestID(r), sourceIP(r), map[string]string{"kind": c.Kind}); e != nil {
				return e
			}
			return a.assertAdminLoginPath(ctx)
		}
		u, e := a.identity.ContactLoginUser(ctx, c)
		if e != nil {
			return e
		}
		if e = a.identity.CheckLock(ctx, u.Username); e != nil {
			return e
		}
		enabled, required, e := a.identity.MFAStatus(ctx, u.ID)
		if e != nil {
			return e
		}
		phase = "authenticated"
		if enabled {
			phase = "mfa_pending"
		} else if required {
			phase = "mfa_enroll"
		}
		u.Phase = phase
		if e = a.identity.Revoke(ctx, token(r)); e != nil {
			return e
		}
		session, e = a.identity.Issue(ctx, u, c.AuthEpoch, sourceIP(r))
		if e != nil {
			return e
		}
		return a.audit.Login(ctx, u.ID, "sms_"+phase, requestID(r), sourceIP(r))
	})
	if e != nil {
		return e
	}
	if session != "" {
		if err := a.setSession(w, r, session, true); err != nil {
			return err
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "passwordReset": in.Purpose == "recovery", "needTotp": phase == "mfa_pending", "needRebind": phase == "mfa_enroll"})
	return nil
}
func (a *App) deliverContactVerification(ctx context.Context) error {
	d, e := a.identity.ClaimContactDelivery(ctx)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	sendCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	e = a.notifications().SendVerification(sendCtx, d.ChannelID, d.ChannelVersion, d.Address, d.ID, d.Body)
	return a.identity.FinishContactDelivery(ctx, d, e == nil)
}
