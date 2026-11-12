package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
)

func (a *App) ldapProviders(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	items, e := a.identity.LDAPProviders(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}
func (a *App) saveLDAPProvider(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		identity.LDAPProvider
		BindPassword string `json:"bindPassword"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	if in.ID != "" && !resource.ValidID(in.ID) {
		return fault.Invalid
	}
	var id string
	err := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		var e error
		id, e = a.identity.SaveLDAPProvider(ctx, in.LDAPProvider, in.BindPassword)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.ldap_provider_saved", requestID(r), sourceIP(r), map[string]string{"id": id})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{"id": id})
	return nil
}
func (a *App) testLDAPProvider(w http.ResponseWriter, r *http.Request) error {
	u, err := a.require(r, "identity.manage")
	if err != nil {
		return err
	}
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	release, err := a.hashPermit(r)
	if err != nil {
		return err
	}
	defer release()
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err = decode(w, r, &in); err != nil {
		return err
	}
	p, err := a.identity.LDAPProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	secret, err := a.identity.LDAPPassword(r.Context(), p.ID, a.config.VaultKey)
	if err != nil {
		return err
	}
	subject, verifyErr := identity.VerifyLDAP(r.Context(), p, secret, in.Username, in.Password)
	status := "succeeded"
	if verifyErr != nil {
		status = "failed"
	}
	if err = a.audit.Detail(r.Context(), u.ID, "identity.ldap_provider_tested", requestID(r), sourceIP(r), map[string]string{"id": p.ID, "status": status}); err != nil {
		return err
	}
	if verifyErr != nil {
		return problem(422, "ldap_verification_failed", "目录验证失败，请检查 TLS 证书、账号密码、Base DN、筛选范围及唯一身份属性")
	}
	writeJSON(w, 200, map[string]any{"subject": subject, "version": p.Version, "message": "目录验证通过；验证操作不会自动绑定或创建平台账号"})
	return nil
}

func (a *App) previewLDAPSync(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		Limit int `json:"limit"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !resource.ValidID(r.PathValue("id")) || in.Limit < 0 || in.Limit > 10000 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var p identity.LDAPProvider
	var secret string
	e = a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		var e error
		p, e = a.identity.LDAPProvider(ctx, r.PathValue("id"))
		if e != nil {
			return e
		}
		secret, e = a.identity.LDAPPassword(ctx, p.ID, a.config.VaultKey)
		return e
	})
	if e != nil {
		return e
	}
	if in.Limit == 0 {
		in.Limit = min(1000, p.DirectoryLimit())
	}
	entries, e := identity.ReadLDAPDirectory(r.Context(), p, secret, in.Limit)
	if e != nil {
		return problem(422, "ldap_sync_failed", "完整目录读取失败；请检查筛选范围、数量上限、分页和 TLS；不会导入部分结果")
	}
	var preview string
	e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		if e := a.allowed(ctx, u, "identity.manage"); e != nil {
			return e
		}
		var e error
		preview, e = a.identity.SaveLDAPPreview(ctx, u.ID, token(r), p, entries)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.ldap_sync_previewed", requestID(r), sourceIP(r), map[string]string{"providerId": p.ID})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"preview": preview, "entries": entries, "version": p.Version, "expiresIn": 300})
	return nil
}
func (a *App) applyLDAPSync(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		Preview        string   `json:"preview"`
		DepartmentID   string   `json:"departmentId"`
		Subjects       []string `json:"subjects"`
		DisableMissing bool     `json:"disableMissing"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.DepartmentID != "" && !resource.ValidID(in.DepartmentID) {
		return fault.Invalid
	}
	var counts map[string]int
	e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		var e error
		counts, e = a.identity.ApplyLDAPPreview(ctx, u.ID, token(r), in.Preview, in.DepartmentID, in.Subjects, in.DisableMissing)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.ldap_directory_synced", requestID(r), sourceIP(r), map[string]string{"disableMissing": strconv.FormatBool(in.DisableMissing)})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, counts)
	return nil
}
