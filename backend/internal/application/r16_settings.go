package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/listener"
	"slices"
	"strconv"
	"strings"
	"time"
)

func configTag(id int64) string { return fmt.Sprintf(`"settings-%d"`, id) }
func configMatch(r *http.Request, required bool) (*int64, error) {
	value := r.Header.Get("If-Match")
	if value == "" {
		if required {
			return nil, problem(428, "settings_version_required", "请刷新配置后再提交")
		}
		return nil, nil
	}
	if !strings.HasPrefix(value, `"settings-`) || !strings.HasSuffix(value, `"`) {
		return nil, fault.Invalid
	}
	n, e := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(value, `"settings-`), `"`), 10, 64)
	if e != nil || n < 1 || value != configTag(n) {
		return nil, fault.Invalid
	}
	return &n, nil
}
func (a *App) registerConfiguration(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	a.registerAccessMFA(h)
	h("GET /api/settings/revisions", a.configurationHistory)
	h("GET /api/settings/revisions/{id}/diff", a.configurationDiff)
	h("GET /api/settings/schema", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "settings.read"); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"scope": "login-password-mfa-listeners-mail-client-ip-postgres-branding-retention-resources-watermark-ai-policy", "properties": system.PropertySchema(), "readOnlyProperties": system.ReadonlyPropertySchema(), "description": "管理登录保护、密码强度与到期、全局 TOTP、登录会话、网关监听与数据库代理证书、加密邮件配置、客户端 IP 与可信代理、PostgreSQL 限制、品牌、资源默认值、会话水印、AI全局工具策略及审计保留；缩短保留期限需要当前版本的清理预览。其他配置使用对应业务入口。"})
		return nil
	})
	view := func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "settings.read"); e != nil {
			return e
		}
		var id int64
		var e error
		if r.PathValue("id") != "" {
			id, e = strconv.ParseInt(r.PathValue("id"), 10, 64)
			if e != nil || id < 1 {
				return fault.Invalid
			}
		}
		v, e := a.system.ConfigurationView(r.Context(), id)
		if e != nil {
			return e
		}
		w.Header().Set("ETag", configTag(v.ID))
		w.Header().Set("X-Settings-Revision", strconv.FormatInt(v.ID, 10))
		writeJSON(w, 200, v.Redacted())
		return nil
	}
	h("GET /api/settings", view)
	h("GET /api/settings/revisions/{id}", view)
	save := func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		legacy := r.URL.Path == "/api/admin/properties"
		expected, e := configMatch(r, !legacy)
		if e != nil {
			return e
		}
		var patch map[string]json.RawMessage
		var proof backupProof
		var clearKeys []string
		if legacy {
			if e = decode(w, r, &patch); e != nil {
				return e
			}
			if raw, ok := patch["reauth"]; ok {
				if json.Unmarshal(raw, &proof) != nil {
					return fault.Invalid
				}
				delete(patch, "reauth")
			}
			if value, ok := patch["access-retention-days"]; ok {
				if _, exists := patch["access-log-saved-limit-days"]; exists {
					return fault.Invalid
				}
				patch["access-log-saved-limit-days"] = value
				delete(patch, "access-retention-days")
			}
		} else {
			var body struct {
				backupProof
				Set          map[string]json.RawMessage `json:"set"`
				ClearSecrets []string                   `json:"clearSecrets"`
			}
			if e = decode(w, r, &body); e != nil {
				return e
			}
			clearKeys = body.ClearSecrets
			proof = body.backupProof
			patch = body.Set
		}
		if patch == nil {
			patch = map[string]json.RawMessage{}
		}
		oauthServerChange := false
		recordingChange := false
		nativePasswordChange := false
		databaseDMLChange := false
		captchaChange := false
		accessMFAChange := false
		forwardChange := false
		federationChange := false
		ldapChange := false
		passkeyChange := false
		oauthLifetimeChange := false
		for key := range patch {
			if key == system.LoginCaptchaKey {
				captchaChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if key == system.DatabaseBlockDMLKey {
				databaseDMLChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if key == system.NativePasswordDisabledKey {
				nativePasswordChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if system.IsAccessMFAConfigurationKey(key) {
				accessMFAChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if system.IsOAuthServerConfigurationKey(key) {
				oauthServerChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if key == "recording-enabled" {
				recordingChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if system.IsForwardConfigurationKey(key) {
				forwardChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if system.IsFederationConfigurationKey(key) {
				federationChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if system.IsOAuthLifetimeKey(key) {
				oauthLifetimeChange = true
				if u.Type != "admin" {
					return fault.Denied
				}
			}
			if strings.HasPrefix(key, "passkey-") {
				passkeyChange = true
			}
			if strings.HasPrefix(key, "ldap-") {
				ldapChange = true
			}
			if (strings.HasPrefix(key, "llm-") || strings.HasPrefix(key, "ldap-") || strings.HasPrefix(key, "passkey-")) && u.Type != "admin" {
				return fault.Denied
			}
		}
		reauth, err := settingsSecretInput(patch, clearKeys)
		if err != nil {
			return err
		}
		_, loginPolicyChange := patch["disable-password-login"]
		for _, key := range clearKeys {
			if system.IsOAuthServerConfigurationKey(key) {
				oauthServerChange = true
			}
			if system.IsFederationConfigurationKey(key) {
				federationChange = true
			}
			if strings.HasPrefix(key, "ldap-") {
				ldapChange = true
			}
		}
		if (ldapChange || federationChange || oauthServerChange) && u.Type != "admin" {
			return fault.Denied
		}
		reauth = reauth || captchaChange || databaseDMLChange || nativePasswordChange || accessMFAChange || oauthServerChange || recordingChange || forwardChange || loginPolicyChange || ldapChange || passkeyChange || oauthLifetimeChange || federationChange
		if reauth && proof.Password == "" {
			return problem(422, "settings_reauthentication_required", "修改秘密或登录入口需要管理员密码复核；已绑定 MFA 时还需新的动态口令")
		}
		var revision system.Revision
		saveChanges := func(ctx context.Context) error {
			var previousOAuth system.Revision
			if oauthServerChange {
				var lockedRevision int64
				if e := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&lockedRevision); e != nil {
					return e
				}
				ctx = system.WithOAuthServerConfigurationProof(ctx)
				current, e := a.system.Configuration(ctx, lockedRevision)
				if e != nil {
					return e
				}
				previousOAuth = current
				rotations := []string{}
				for _, key := range clearKeys {
					if key == system.OAuthSigningKey || key == system.OAuthHMACKey {
						rotations = append(rotations, key)
					}
				}
				ctx = system.WithOAuthKeyRotation(ctx, rotations)
				issuer := current.Values.String(system.OAuthServerIssuerKey)
				if raw, ok := patch[system.OAuthServerIssuerKey]; ok {
					if json.Unmarshal(raw, &issuer) != nil {
						return fault.Invalid
					}
				}
				if issuer != "" && !slices.Contains(a.config.AllowedOrigins, issuer) {
					return fault.Invalid
				}
				if e := a.identity.EnsureOAuthKey(ctx, false); e != nil {
					return e
				}
			}
			if databaseDMLChange {
				ctx = system.WithDatabaseDMLConfigurationProof(ctx)
			}
			if captchaChange {
				ctx = system.WithCaptchaConfigurationProof(ctx)
			}
			if nativePasswordChange {
				ctx = system.WithNativePasswordConfigurationProof(ctx)
			}
			if accessMFAChange {
				ctx = system.WithAccessMFAConfigurationProof(ctx)
			}
			if recordingChange {
				ctx = system.WithRecordingConfigurationProof(ctx)
			}
			if forwardChange {
				ctx = system.WithForwardConfigurationProof(ctx)
			}
			if federationChange {
				ctx = system.WithFederationConfigurationProof(ctx)
			}
			if oauthLifetimeChange {
				ctx = system.WithOAuthLifetimeProof(ctx)
			}
			if passkeyChange {
				ctx = system.WithPasskeyConfigurationProof(ctx)
			}
			if ldapChange {
				ctx = system.WithLDAPConfigurationProof(ctx)
			}
			if loginPolicyChange {
				if e := a.validateLoginConfiguration(ctx, patch, u); e != nil {
					return e
				}
				ctx = system.WithLoginPolicyProof(ctx)
			}
			if legacy {
				if e := a.system.NormalizeLegacyReadonly(ctx, patch); e != nil {
					return e
				}
			}
			if e := a.prepareConfigurationSecrets(ctx, patch, clearKeys, u.ID); e != nil {
				return e
			}
			var err error
			revision, err = a.system.SaveConfiguration(ctx, expected, patch, u.ID)
			if err != nil {
				return err
			}
			if oauthServerChange {
				if e := a.applyOAuthKeyConfiguration(ctx, previousOAuth.Values, revision.Values); e != nil {
					return e
				}
			}
			if passkeyChange && revision.Values.Bool("passkey-enabled") {
				if _, e := a.configuredPasskey(revision.Values); e != nil {
					return e
				}
			}
			if loginPolicyChange || passkeyChange || federationChange {
				if e := a.assertAdminLoginPath(ctx); e != nil {
					return e
				}
			}
			return a.audit.Detail(ctx, u.ID, "settings.update", requestID(r), sourceIP(r), map[string]string{"revision": strconv.FormatInt(revision.ID, 10), "legacy": strconv.FormatBool(legacy)})
		}
		if reauth {
			e = a.withPasswordProof(r, proof, "identity.manage", func(ctx context.Context, verified *identity.User) error {
				if oauthServerChange && verified.Type != "admin" {
					return fault.Denied
				}
				return saveChanges(ctx)
			})
		} else {
			e = a.db.Within(r.Context(), saveChanges)
		}
		if e != nil {
			return e
		}
		w.Header().Set("ETag", configTag(revision.ID))
		w.Header().Set("X-Settings-Revision", strconv.FormatInt(revision.ID, 10))
		if legacy {
			w.WriteHeader(204)
		} else {
			writeJSON(w, 202, revision.Redacted())
		}
		return nil
	}
	h("PATCH /api/settings", save)
	h("PUT /api/admin/properties", save)
	h("POST /api/settings/revisions/{id}/retry", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		expected, e := configMatch(r, true)
		if e != nil {
			return e
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || id < 1 {
			return fault.Invalid
		}
		if id != *expected {
			return fault.Conflict
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.system.RetryConfiguration(ctx, id); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "settings.retry", requestID(r), sourceIP(r), map[string]string{"revision": strconv.FormatInt(id, 10)})
		})
		if e != nil {
			return e
		}
		w.Header().Set("ETag", configTag(id))
		w.WriteHeader(202)
		return nil
	})
}
func (a *App) appliedConfiguration(ctx context.Context) (system.Revision, error) {
	a.configurationMu.Lock()
	defer a.configurationMu.Unlock()
	var prepared *listener.Prepared
	var publicPrepared *listener.Prepared
	defer func() {
		if publicPrepared != nil {
			publicPrepared.Abort()
		}
	}()
	var certificateID string
	v, err := a.system.ApplyConfiguration(ctx, a.configurationNode, func(v system.Revision) error {
		if err := system.ValidateConfiguration(v.Values); err != nil {
			return err
		}
		if a.publicListeners != nil {
			var e error
			publicPrepared, e = a.publicListeners.Prepare(ctx, a.publicListenerSpecs(v.Values))
			if e != nil {
				return e
			}
		}
		if a.listenerManager == nil {
			return nil
		}
		var err error
		prepared, certificateID, err = a.prepareListeners(ctx, v)
		return err
	})
	if prepared != nil {
		defer prepared.Abort()
		if err == nil {
			err = prepared.Commit()
		}
	}
	if err == nil && a.listenerManager != nil {
		a.refreshListenerReadiness(v.Values)
		if certificateID != "" {
			a.databaseProxyCertificate.Store(certificateID)
		}
	}
	if err == nil && publicPrepared != nil {
		err = publicPrepared.Commit()
	}
	return v, err
}
func (a *App) RunConfiguration(ctx context.Context) {
	m := listener.New(ctx, 192)
	a.configurationMu.Lock()
	a.listenerManager = m
	a.configurationMu.Unlock()
	defer func() {
		m.Close()
		m.Wait()
		a.configurationMu.Lock()
		a.listenerManager = nil
		a.nativeSSHReady.Store(false)
		a.databaseProxyReady.Store(false)
		a.postgresProxyReady.Store(false)
		if a.managedRDP != nil {
			a.nativeRDPAddress.Store(nil)
		}
		a.configurationMu.Unlock()
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_, _ = a.db.Exec(cleanup, `UPDATE configuration_nodes SET last_seen=now()-interval '31 seconds' WHERE id=$1`, a.configurationNode)
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		c, done := context.WithTimeout(ctx, 3*time.Second)
		_, _ = a.appliedConfiguration(c)
		done()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
