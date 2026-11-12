package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
)

const passkeyCookie = "nt_passkey_challenge"

func (a *App) startPasskeyLogin(w http.ResponseWriter, r *http.Request) error {
	_, e := a.passkeyProtocol(r.Context())
	if e != nil {
		return e
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var in struct {
		Key string `json:"key"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if e = a.checkLoginCaptcha(r, in.Key); e != nil {
		return e
	}
	binding := identity.NewToken()
	var challenge string
	var options *protocol.CredentialAssertion
	e = a.db.Within(r.Context(), func(c context.Context) error {
		current, e := a.lockedPasskeyProtocol(c)
		if e != nil {
			return e
		}
		var session *webauthn.SessionData
		options, session, e = current.BeginLogin(r.Header.Get("Origin"))
		if e != nil {
			return e
		}
		challenge, e = a.identity.SavePasskeyCeremony(c, "", "login", binding, session)
		return e
	})
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: binding, Path: "/api/passkey", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 300})
	writeJSON(w, 200, map[string]any{"token": challenge, "options": options.Response})
	return nil
}
func (a *App) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) error {
	p, e := a.passkeyProtocol(r.Context())
	if e != nil {
		return e
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var in struct {
		Token    string          `json:"token"`
		Response json.RawMessage `json:"response"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	cookie, e := r.Cookie(passkeyCookie)
	if e != nil {
		return fault.Proof
	}
	session, e := a.identity.ConsumePasskeyCeremony(r.Context(), in.Token, cookie.Value, "login", "")
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: "", Path: "/api/passkey", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	if session.Origin != r.Header.Get("Origin") {
		return fault.Proof
	}
	parsed, e := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(in.Response))
	if e != nil {
		return fault.Proof
	}
	var sessionToken string
	phase := "authenticated"
	e = a.db.Within(r.Context(), func(c context.Context) error {
		var subject identity.PasskeyUser
		credential, e := p.FinishLogin(func(raw, handle []byte) (webauthn.User, error) {
			var err error
			subject, err = a.identity.PasskeyLoginUser(c, p.RPID(), raw, handle)
			if err != nil {
				return subject, err
			}
			current, err := a.lockedPasskeyProtocol(c)
			if err != nil {
				return subject, err
			}
			if !current.SessionAllowed(session) {
				return subject, fault.Proof
			}
			return subject, nil
		}, session, parsed)
		if e != nil {
			return fault.Proof
		}
		if e = a.identity.CheckLock(c, subject.Username); e != nil {
			return e
		}
		if e = a.identity.UpdatePasskeyLogin(c, subject.ID, p.RPID(), credential); e != nil {
			return e
		}
		enabled, required, e := a.identity.MFAStatus(c, subject.ID)
		if e != nil {
			return e
		}
		if required {
			return fault.Proof
		}
		if enabled {
			phase = "mfa_pending"
		}
		state, e := a.system.State(c)
		if e != nil {
			return e
		}
		if !state.Initialized {
			return fault.Proof
		}
		subject.User.PasskeyID, e = a.identity.PasskeyRecordID(c, subject.ID, p.RPID(), credential.ID)
		if e != nil {
			return e
		}
		subject.User.Phase = phase
		if e = a.identity.Revoke(c, token(r)); e != nil {
			return e
		}
		sessionToken, e = a.identity.Issue(c, subject.User, state.AuthEpoch, sourceIP(r))
		if e != nil {
			return e
		}
		return a.audit.Login(c, subject.ID, "passkey_"+phase, requestID(r), sourceIP(r))
	})
	if e != nil {
		return e
	}
	if err := a.setSession(w, r, sessionToken, true); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"needTotp": phase == "mfa_pending"})
	return nil
}
