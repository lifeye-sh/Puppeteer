package application

import (
	"context"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
)

// Explicit local development fixture. TEST-NET hosts are examples, not verified SSH targets.
func (a *App) SeedR03(ctx context.Context, operator string) (map[string]string, error) {
	result := map[string]string{}
	e := a.db.Within(ctx, func(ctx context.Context) error {
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
		target, e := a.identity.FindByName(ctx, "operator")
		if e != nil {
			return e
		}
		credentials, e := a.vault.List(ctx)
		if e != nil {
			return e
		}
		credential := ""
		for _, c := range credentials {
			if c.Name == "R03 示例共享凭据" {
				credential = c.ID
			}
		}
		if credential == "" {
			credential, e = a.vault.Save(ctx, vault.Write{Credential: vault.Credential{Name: "R03 示例共享凭据", Kind: "password", Username: "demo"}, Secret: &vault.Secret{Password: identity.NewToken()}})
			if e != nil {
				return e
			}
		}
		groups, e := a.resources.Groups(ctx)
		if e != nil {
			return e
		}
		group := ""
		for _, g := range groups {
			if g.Name == "R03 验收资产" {
				group = g.ID
			}
		}
		if group == "" {
			group, e = a.resources.SaveGroup(ctx, resource.Group{Name: "R03 验收资产"})
			if e != nil {
				return e
			}
		}
		for i, name := range []string{"R03 SSH A", "R03 SSH B"} {
			items, _, e := a.resources.List(ctx, resource.Filter{All: true, Search: name, Limit: 100})
			if e != nil {
				return e
			}
			id := ""
			for _, v := range items {
				if v.Name == name {
					id = v.ID
				}
			}
			if id == "" {
				host := "192.0.2.10"
				alias := "r03-ssh-a"
				if i == 1 {
					host = "192.0.2.11"
					alias = "r03-ssh-b"
				}
				id, e = a.saveAsset(ctx, resource.Asset{Name: name, Protocol: "ssh", Alias: alias, Host: host, Port: 22, GroupID: group, CredentialID: credential, Enabled: true, ConnectTimeout: 10, Description: "TEST-NET 示例地址，请编辑为你有权访问的真实 SSH 主机；本轮只测试 TCP", Tags: []string{"r03", "example"}})
				if e != nil {
					return e
				}
			}
			result[name] = id
		}
		grants, e := a.authz.Grants(ctx)
		if e != nil {
			return e
		}
		exists := false
		for _, g := range grants {
			if g.UserID == target.ID && g.ResourceID == result["R03 SSH A"] {
				exists = true
			}
		}
		if !exists {
			if _, e = a.authz.SaveGrant(ctx, authorization.Grant{UserID: target.ID, ResourceID: result["R03 SSH A"]}); e != nil {
				return e
			}
		}
		result["credentialId"] = credential
		result["groupId"] = group
		return a.audit.Detail(ctx, actor.ID, "fixture.r03_ready", "cli", "local", map[string]string{"target": target.ID})
	})
	return result, e
}
