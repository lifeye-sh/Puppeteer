package application

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"unicode/utf8"
)

// This operator identity is declared by a privileged local CLI caller, not a browser authentication.
func (a *App) ResetMFAFromCLI(ctx context.Context, username, operator, reason string) error {
	if !usernamePattern.MatchString(username) || !validName(operator) || utf8.RuneCountInString(strings.TrimSpace(reason)) < 5 || utf8.RuneCountInString(reason) > 500 {
		return fault.Invalid
	}
	return a.db.Within(ctx, func(ctx context.Context) error {
		if e := a.identity.LockManagement(ctx); e != nil {
			return e
		}
		u, e := a.identity.FindByName(ctx, strings.ToLower(username))
		if e != nil {
			return e
		}
		if e = a.identity.ResetMFA(ctx, u.ID); e != nil {
			return e
		}
		return a.audit.Detail(ctx, "", "mfa.cli_reset", "cli", "local", map[string]string{"target": u.ID, "operator": operator, "reason": reason})
	})
}
