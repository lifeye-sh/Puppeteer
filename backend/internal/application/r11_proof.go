package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"slices"
)

type backupProof struct {
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

func (a *App) withBackupProof(r *http.Request, in backupProof, fn func(context.Context, *identity.User) error) error {
	return a.withPasswordProof(r, in, "backup.export", fn)
}
func (a *App) withPasswordProof(r *http.Request, in backupProof, permission string, fn func(context.Context, *identity.User) error) error {
	return a.withPasswordProofPhases(r, in, permission, []string{"authenticated"}, fn)
}
func (a *App) withPasswordProofPhases(r *http.Request, in backupProof, permission string, phases []string, fn func(context.Context, *identity.User) error) error {
	if len(in.Password) > 1024 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	failed := false
	e = a.securityTx(r, func(c context.Context, u *identity.User) error {
		if !slices.Contains(phases, u.Phase) {
			return fault.Denied
		}
		if e := func() error {
			if permission == "" {
				return nil
			}
			return a.allowed(c, u, permission)
		}(); e != nil {
			return e
		}
		if e := a.identity.CheckLock(c, u.Username); e != nil {
			return e
		}
		verified, e := a.identity.Verify(c, u.Username, in.Password)
		if e != nil {
			return e
		}
		if verified == nil {
			failed = true
			return a.identity.FailedPassword(c, u.Username)
		}
		enabled, _, e := a.identity.MFAStatus(c, u.ID)
		if e != nil {
			return e
		}
		if enabled {
			if e = a.identity.ConsumeTOTP(c, u.ID, in.TOTP); e != nil {
				failed = true
				return a.identity.FailedPassword(c, u.Username)
			}
		}
		if e = a.identity.Unlock(c, u.Username); e != nil {
			return e
		}
		return fn(c, u)
	})
	if e != nil {
		return e
	}
	if failed {
		return fault.Proof
	}
	return nil
}
