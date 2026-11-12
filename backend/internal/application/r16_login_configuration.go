package application

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) validateLoginConfiguration(ctx context.Context, patch map[string]json.RawMessage, u *identity.User) error {
	raw, ok := patch["disable-password-login"]
	if !ok {
		return nil
	}
	if u.Type != "admin" {
		return fault.Denied
	}
	var disable bool
	if string(raw) == "null" {
		return fault.Invalid
	}
	if json.Unmarshal(raw, &disable) != nil {
		var text string
		if json.Unmarshal(raw, &text) != nil || (text != "true" && text != "false") {
			return fault.Invalid
		}
		disable = text == "true"
	}
	// Serialize policy edits before checking alternate identities. The final
	// invariant uses the same advisory lock as provider/credential revocation.
	if _, e := a.db.Exec(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`); e != nil {
		return e
	}
	if disable {
		count, e := a.identity.AdminLoginAlternatives(ctx, u.ID, true, a.policyRP(ctx))
		if e != nil {
			return e
		}
		if count == 0 {
			return problem(409, "alternate_login_required", "请先在 15 分钟内使用当前管理员的备用登录方式完成登录及 MFA")
		}
	}
	return nil
}
