package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"slices"
)

type FixtureUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Explicit local CLI only. Existing accounts are verified, never overwritten.
func (a *App) SeedR02(ctx context.Context, operator string, input []FixtureUser) error {
	if len(input) != 3 {
		return fault.Invalid
	}
	passwords := map[string]string{}
	for _, u := range input {
		if !slices.Contains([]string{"operator", "reviewer", "denied"}, u.Username) || len(u.Password) < 12 || len(u.Password) > 1024 {
			return fault.Invalid
		}
		passwords[u.Username] = u.Password
	}
	if len(passwords) != 3 {
		return fault.Invalid
	}
	return a.db.Within(ctx, func(ctx context.Context) error {
		if e := a.identity.LockManagement(ctx); e != nil {
			return e
		}
		actor, e := a.identity.FindByName(ctx, operator)
		if e != nil {
			return e
		}
		if actor.Type != "admin" {
			return fault.Denied
		}
		deps, e := a.identity.Departments(ctx)
		if e != nil {
			return e
		}
		depIDs := []string{}
		for _, name := range []string{"R02 运维部", "R02 审计部"} {
			id := ""
			for _, d := range deps {
				if d.Name == name && d.ParentID == "" {
					id = d.ID
				}
			}
			if id == "" {
				id, e = a.identity.SaveDepartment(ctx, identity.Department{Name: name})
				if e != nil {
					return e
				}
			}
			depIDs = append(depIDs, id)
		}
		roles, e := a.authz.Roles(ctx)
		if e != nil {
			return e
		}
		roleIDs := []string{}
		for _, spec := range []authorization.Role{{Name: "R02 Operator", Permissions: []string{"department.read", "user.read"}}, {Name: "R02 Reviewer", Permissions: []string{"audit.read", "role.read"}}} {
			id := ""
			for _, r := range roles {
				if r.Name == spec.Name {
					if !slices.Equal(r.Permissions, spec.Permissions) {
						return fault.Conflict
					}
					id = r.ID
				}
			}
			if id == "" {
				id, e = a.authz.Save(ctx, spec)
				if e != nil {
					return e
				}
			}
			roleIDs = append(roleIDs, id)
		}
		for i, name := range []string{"operator", "reviewer", "denied"} {
			_, e := a.identity.FindByName(ctx, name)
			if e == nil {
				verified, e := a.identity.Verify(ctx, name, passwords[name])
				if e != nil {
					return e
				}
				if verified == nil {
					return fault.Conflict
				}
				continue
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			departments, roles := []string{}, []string{}
			if i < 2 {
				departments = []string{depIDs[i]}
				roles = []string{roleIDs[i]}
			}
			id, e := a.identity.SaveUser(ctx, identity.ManagedUser{Username: name, Nickname: name, Type: "user", Status: "enabled", DepartmentIDs: departments}, identity.HashPassword(passwords[name]))
			if e != nil {
				return e
			}
			if e = a.authz.Assign(ctx, id, roles); e != nil {
				return e
			}
			if e = a.audit.Detail(ctx, actor.ID, "fixture.user_created", "cli", "local", map[string]string{"target": id, "username": name}); e != nil {
				return e
			}
		}
		return nil
	})
}
