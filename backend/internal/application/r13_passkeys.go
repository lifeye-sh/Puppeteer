package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/go-webauthn/webauthn/protocol"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/system"
	"slices"
)

func (a *App) configuredPasskey(v system.ConfigurationValues) (*identity.PasskeyProtocol, error) {
	rp, origins, e := system.PasskeyConfiguration(v)
	if e != nil {
		return nil, e
	}
	if rp == "" {
		rp, origins = a.config.PasskeyRPID, a.config.PasskeyOrigins
	}
	if rp == "" {
		return nil, problem(503, "passkey_disabled", "尚未配置 Passkey 的 RP ID 与 HTTPS 来源")
	}
	for _, origin := range origins {
		if !slices.Contains(a.config.AllowedOrigins, origin) {
			return nil, problem(422, "passkey_origin_not_allowed", "Passkey 来源还须加入部署的 API Origin 白名单")
		}
	}
	return identity.NewPasskeyProtocol(rp, origins)
}
func (a *App) passkeyProtocol(ctx context.Context) (*identity.PasskeyProtocol, error) {
	v, e := a.system.SecurityConfiguration(ctx)
	if e != nil {
		return nil, e
	}
	if !v.Values.Bool("passkey-enabled") {
		return nil, problem(503, "passkey_disabled", "管理员已关闭 Passkey 登录与注册")
	}
	return a.configuredPasskey(v.Values)
}

// Call only after the user row lock, matching authenticated settings writes.
func (a *App) lockedPasskeyProtocol(ctx context.Context) (*identity.PasskeyProtocol, error) {
	if _, e := a.db.Exec(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`); e != nil {
		return nil, e
	}
	return a.passkeyProtocol(ctx)
}
func (a *App) passkeyEnabled(ctx context.Context) bool {
	_, e := a.passkeyProtocol(ctx)
	return e == nil
}
func (a *App) listPasskeys(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	v, e := a.system.Configuration(r.Context(), 0)
	if e != nil {
		return e
	}
	p, e := a.configuredPasskey(v.Values)
	if e != nil {
		return e
	}
	items, e := a.identity.Passkeys(r.Context(), u.ID, p.RPID())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}
func (a *App) startPasskeyRegistration(w http.ResponseWriter, r *http.Request) error {
	_, e := a.passkeyProtocol(r.Context())
	if e != nil {
		return e
	}
	var in backupProof
	if e = decode(w, r, &in); e != nil {
		return e
	}
	var options any
	var challenge string
	e = a.withPasswordProof(r, in, "", func(c context.Context, u *identity.User) error {
		p, e := a.lockedPasskeyProtocol(c)
		if e != nil {
			return e
		}
		subject, e := a.identity.PasskeyUser(c, u.ID, p.RPID())
		if e != nil {
			return e
		}
		creation, session, e := p.BeginRegistration(subject, r.Header.Get("Origin"))
		if e != nil {
			return e
		}
		challenge, e = a.identity.SavePasskeyCeremony(c, u.ID, "register", token(r), session)
		options = creation.Response
		return e
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"token": challenge, "options": options})
	return nil
}
func (a *App) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	_, e = a.passkeyProtocol(r.Context())
	if e != nil {
		return e
	}
	var in struct {
		Token    string          `json:"token"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	session, e := a.identity.ConsumePasskeyCeremony(r.Context(), in.Token, token(r), "register", u.ID)
	if e != nil {
		return e
	}
	if session.Origin != r.Header.Get("Origin") {
		return problem(403, "passkey_origin", "来源不匹配")
	}
	parsed, e := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(in.Response))
	if e != nil {
		return problem(400, "passkey_invalid", "Passkey 注册响应无效，请重新开始")
	}
	var id string
	e = a.securityTx(r, func(c context.Context, current *identity.User) error {
		if current.Phase != "authenticated" {
			return problem(403, "mfa_required", "请完成身份验证")
		}
		p, e := a.lockedPasskeyProtocol(c)
		if e != nil {
			return e
		}
		subject, e := a.identity.PasskeyUser(c, current.ID, p.RPID())
		if e != nil {
			return e
		}
		credential, e := p.FinishRegistration(subject, session, parsed)
		if e != nil {
			return problem(400, "passkey_invalid", "Passkey 签名验证失败，请重新开始")
		}
		id, e = a.identity.SavePasskey(c, current.ID, p.RPID(), in.Name, credential)
		if e != nil {
			return e
		}
		return a.audit.Detail(c, current.ID, "account.passkey_created", requestID(r), sourceIP(r), map[string]string{"id": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 201, map[string]string{"id": id})
	return nil
}
func (a *App) revokePasskey(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return problem(400, "invalid_id", "无效凭据 ID")
	}
	var in backupProof
	if e := decode(w, r, &in); e != nil {
		return e
	}
	e := a.withPasswordProof(r, in, "", func(c context.Context, u *identity.User) error {
		if e := a.identity.RevokePasskey(c, u.ID, id); e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "account.passkey_revoked", requestID(r), sourceIP(r), map[string]string{"id": id})
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
