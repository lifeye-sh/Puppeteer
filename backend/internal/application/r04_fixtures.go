package application

import (
	"context"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/fault"
)

// Opt-in dev fixture, separate from user-edited R03 examples. Private key remains encrypted in Vault.
func (a *App) SeedR04(ctx context.Context, operator string) (map[string]string, error) {
	out := map[string]string{}
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
		creds, e := a.vault.List(ctx)
		if e != nil {
			return e
		}
		id := ""
		for _, c := range creds {
			if c.Name == "R04 本地 SSH 密钥" {
				id = c.ID
			}
		}
		if id == "" {
			id, e = a.vault.Save(ctx, vault.Write{Credential: vault.Credential{Name: "R04 本地 SSH 密钥", Username: "tester", Kind: "private-key"}, Generate: true})
			if e != nil {
				return e
			}
		}
		cred, e := a.vault.Get(ctx, id)
		if e != nil {
			return e
		}
		if cred.PublicKey == "" {
			return fault.Conflict
		}
		out["publicKey"] = cred.PublicKey
		out["credentialId"] = id
		assets, _, e := a.resources.List(ctx, resource.Filter{All: true, Search: "R04 本地 SSH", Limit: 100})
		if e != nil {
			return e
		}
		asset := ""
		for _, v := range assets {
			if v.Name == "R04 本地 SSH" {
				asset = v.ID
			}
		}
		if asset == "" {
			asset, e = a.saveAsset(ctx, resource.Asset{Name: "R04 本地 SSH", Alias: "r04-local-ssh", Protocol: "ssh", Host: "ssh-fixture", Port: 2222, CredentialID: id, Enabled: true, ConnectTimeout: 5, Description: "独立 Docker OpenSSH 验收主机；仅测试用途，不挂载宿主业务文件", Tags: []string{"r04", "local-test"}})
			if e != nil {
				return e
			}
		}
		out["assetId"] = asset
		grants, e := a.authz.Grants(ctx)
		if e != nil {
			return e
		}
		found := false
		for _, g := range grants {
			if g.UserID == target.ID && g.ResourceID == asset {
				found = true
			}
		}
		if !found {
			if _, e = a.authz.SaveGrant(ctx, authorization.Grant{UserID: target.ID, ResourceID: asset}); e != nil {
				return e
			}
		}
		return a.audit.Detail(ctx, actor.ID, "fixture.r04_ready", "cli", "local", map[string]string{"assetId": asset})
	})
	return out, e
}
