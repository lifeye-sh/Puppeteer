package application

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"slices"
	"strings"
)

func oauthError(w http.ResponseWriter, code string, status int) error {
	writeJSON(w, status, map[string]string{"error": code})
	return nil
}
func (a *App) oauthServerSettings(w http.ResponseWriter, r *http.Request) error {
	user, err := a.require(r, "identity.manage")
	if err != nil {
		return err
	}
	if user.Type != "admin" {
		return fault.Denied
	}
	if r.Method == "GET" {
		var v identity.OAuthSettings
		var revision system.Revision
		e := a.db.Within(r.Context(), func(ctx context.Context) error {
			var id int64
			if e := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&id); e != nil {
				return e
			}
			var e error
			revision, e = a.system.Configuration(ctx, id)
			if e != nil {
				return e
			}
			v, e = a.identity.OAuthSettings(ctx)
			return e
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, struct {
			identity.OAuthSettings
			DesiredEnabled        bool  `json:"desiredEnabled"`
			DesiredNativeEnabled  bool  `json:"desiredNativeEnabled"`
			ConfigurationRevision int64 `json:"configurationRevision"`
			Pending               bool  `json:"pending"`
		}{v, revision.Values.Bool(system.OAuthServerEnabledKey), revision.Values.Bool(system.NativeClientEnabledKey), revision.ID,
			v.Enabled != revision.Values.Bool(system.OAuthServerEnabledKey) || v.NativeEnabled != revision.Values.Bool(system.NativeClientEnabledKey)})
		return nil
	}
	var in struct {
		backupProof
		identity.OAuthSettings
		RotateKey bool `json:"rotateKey"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !slices.Contains(a.config.AllowedOrigins, in.Issuer) {
		return fault.Invalid
	}
	e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		if u.Type != "admin" {
			return fault.Denied
		}
		var lockedRevision int64
		if e := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&lockedRevision); e != nil {
			return e
		}
		if e := a.identity.EnsureOAuthKey(ctx, false); e != nil {
			return e
		}
		if e := a.identity.SaveOAuthSettings(system.WithOAuthServerConfigurationProof(ctx), in.OAuthSettings, u.ID); e != nil {
			return e
		}
		if in.RotateKey {
			before, e := a.system.Configuration(ctx, 0)
			if e != nil {
				return e
			}
			rotate := system.WithOAuthKeyRotation(system.WithOAuthServerConfigurationProof(ctx), []string{system.OAuthSigningKey})
			after, e := a.system.SaveConfiguration(rotate, nil, map[string]json.RawMessage{system.OAuthSigningKey: json.RawMessage(`""`)}, u.ID)
			if e != nil {
				return e
			}
			if e = a.applyOAuthKeyConfiguration(rotate, before.Values, after.Values); e != nil {
				return e
			}
		}
		return a.audit.Detail(ctx, u.ID, "identity.oauth_server_saved", requestID(r), sourceIP(r), map[string]string{"issuer": in.Issuer})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) oauthClients(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	items, e := a.identity.OAuthClients(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}
func (a *App) saveOAuthClient(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		backupProof
		identity.OAuthClient
		RotateSecret bool `json:"rotateSecret"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.ID = r.PathValue("id")
	if in.ID != "" && !resource.ValidID(in.ID) {
		return fault.Invalid
	}
	var id, secret string
	e := a.withPasswordProof(r, in.backupProof, "identity.manage", func(ctx context.Context, u *identity.User) error {
		var e error
		id, secret, e = a.identity.SaveOAuthClient(ctx, in.OAuthClient, in.RotateSecret)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "identity.oauth_client_saved", requestID(r), sourceIP(r), map[string]string{"id": id})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]string{"id": id, "secret": secret})
	return nil
}
func (a *App) oauthDiscovery(w http.ResponseWriter, r *http.Request) error {
	s, e := a.identity.OAuthSettings(r.Context())
	if e != nil {
		return e
	}
	if !s.Enabled && !s.NativeEnabled {
		return oauthError(w, "temporarily_unavailable", 503)
	}
	base := s.Issuer + "/api/oidc/server"
	writeJSON(w, 200, map[string]any{"issuer": s.Issuer, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token", "userinfo_endpoint": base + "/userinfo", "jwks_uri": base + "/jwks", "revocation_endpoint": base + "/revoke", "introspection_endpoint": base + "/introspect", "response_types_supported": []string{"code"}, "response_modes_supported": []string{"query"}, "grant_types_supported": []string{"authorization_code", "refresh_token", "client_credentials"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic", "none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"openid", "profile", "email", "phone", "groups", "offline_access", "service", "assets:read", "sessions:connect"}})
	return nil
}
func (a *App) oauthJWKS(w http.ResponseWriter, r *http.Request) error {
	keys, e := a.identity.OAuthJWKS(r.Context())
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"keys": keys})
	return nil
}

var oauthChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var oauthVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func (a *App) oauthAuthorize(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.RawQuery) > 8192 {
		return oauthError(w, "invalid_request", 400)
	}
	q := r.URL.Query()
	for _, v := range q {
		if len(v) != 1 {
			return oauthError(w, "invalid_request", 400)
		}
	}
	if q.Get("client_id") != identity.NativeClientID && (!resource.ValidID(q.Get("client_id")) || q.Get("client_id") == identity.NativeClientUUID) {
		return oauthError(w, "invalid_client", 400)
	}
	clientID := q.Get("client_id")
	if clientID == identity.NativeClientID {
		clientID = identity.NativeClientUUID
	}
	c, e := a.identity.OAuthClient(r.Context(), clientID, false)
	redirectOK := slices.Contains(c.RedirectURIs, q.Get("redirect_uri"))
	if c.Native() {
		redirectOK = identity.NativeRedirect(q.Get("redirect_uri"))
	}
	if e != nil || !c.Enabled || !redirectOK {
		return oauthError(w, "invalid_request", 400)
	}
	scopes := strings.Fields(q.Get("scope"))
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !oauthChallengePattern.MatchString(q.Get("code_challenge")) || len(scopes) > 6 || len(scopes) == 0 || (!c.Native() && !slices.Contains(scopes, "openid")) || len(q.Get("state")) == 0 || len(q.Get("state")) > 2048 || (!c.Native() && len(q.Get("nonce")) == 0) || len(q.Get("nonce")) > 255 || (q.Get("response_mode") != "" && q.Get("response_mode") != "query") || q.Has("request") || q.Has("request_uri") || q.Has("max_age") {
		return oauthError(w, "invalid_request", 400)
	}
	for _, scope := range scopes {
		if scope == "service" || !slices.Contains(c.Scopes, scope) {
			return oauthError(w, "invalid_scope", 400)
		}
	}
	server, e := a.identity.OAuthSettings(r.Context())
	if e != nil {
		return e
	}
	if !server.AllowsClient(c) {
		return oauthError(w, "temporarily_unavailable", 503)
	}
	u, e := a.identity.Current(r.Context(), token(r))
	if e != nil {
		return e
	}
	if q.Get("prompt") == "none" {
		callback, _ := url.Parse(q.Get("redirect_uri"))
		v := callback.Query()
		v.Set("error", "consent_required")
		v.Set("state", q.Get("state"))
		callback.RawQuery = v.Encode()
		http.Redirect(w, r, callback.String(), 303)
		return nil
	}
	if q.Get("prompt") != "" && q.Get("prompt") != "consent" {
		return oauthError(w, "invalid_request", 400)
	}
	if u == nil {
		http.Redirect(w, r, "/login?return="+url.QueryEscape(r.URL.RequestURI()), 303)
		return nil
	}
	allowed, e := a.identity.OAuthUserAllowed(r.Context(), c, u.ID)
	if e != nil {
		return e
	}
	if !allowed {
		return oauthError(w, "access_denied", 403)
	}
	state, e := a.system.State(r.Context())
	if e != nil {
		return e
	}
	var pending string
	e = a.securityTx(r, func(ctx context.Context, current *identity.User) error {
		if current.Phase != "authenticated" {
			return fault.Denied
		}
		var e error
		pending, e = a.identity.SaveOAuthPending(ctx, u.ID, token(r), c, server, state.AuthEpoch, identity.OAuthAuthorization{ClientID: c.ID, RedirectURI: q.Get("redirect_uri"), Scopes: scopes, State: q.Get("state"), Nonce: q.Get("nonce"), Challenge: q.Get("code_challenge")})
		return e
	})
	if e != nil {
		return e
	}
	http.Redirect(w, r, "/oidc/server/consent?request="+pending, 303)
	return nil
}
func (a *App) oauthConsent(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	key := r.URL.Query().Get("request")
	approve := false
	if r.Method == "POST" {
		var in struct {
			Request string `json:"request"`
			Approve bool   `json:"approve"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		key = in.Request
		approve = in.Approve
	}
	p, e := a.identity.OAuthPending(r.Context(), key, token(r), r.Method == "POST")
	if e != nil {
		return e
	}
	if p.UserID != u.ID {
		return fault.Proof
	}
	c, e := a.identity.OAuthClient(r.Context(), p.ClientID, false)
	if e != nil {
		return e
	}
	if r.Method == "GET" {
		writeJSON(w, 200, map[string]any{"name": c.Name, "redirectUri": p.Request.RedirectURI, "scopes": p.Request.Scopes})
		return nil
	}
	callback, _ := url.Parse(p.Request.RedirectURI)
	query := callback.Query()
	query.Set("state", p.Request.State)
	if !approve {
		query.Set("error", "access_denied")
	} else {
		var code string
		e = a.securityTx(r, func(ctx context.Context, current *identity.User) error {
			if current.Phase != "authenticated" || current.ID != p.UserID {
				return fault.Proof
			}
			c, e := a.identity.OAuthClient(ctx, p.ClientID, true)
			if e != nil {
				return e
			}
			s, e := a.identity.OAuthSettings(ctx)
			if e != nil {
				return e
			}
			state, e := a.system.State(ctx)
			if e != nil {
				return e
			}
			allowed, e := a.identity.OAuthUserAllowed(ctx, c, current.ID)
			if e != nil {
				return e
			}
			if !c.Enabled || c.Version != p.ClientVersion || !s.AllowsClient(c) || s.Version != p.ServerVersion || state.AuthEpoch != p.AuthEpoch || !allowed {
				return fault.Proof
			}
			version, e := a.identity.UserVersion(ctx, current.ID)
			if e != nil {
				return e
			}
			code, e = a.identity.OAuthCode(ctx, p, version)
			if e != nil {
				return e
			}
			return a.audit.Detail(ctx, current.ID, "identity.oauth_consent", requestID(r), sourceIP(r), map[string]string{"clientId": c.ID})
		})
		if e != nil {
			return e
		}
		query.Set("code", code)
	}
	callback.RawQuery = query.Encode()
	writeJSON(w, 200, map[string]string{"redirectUrl": callback.String()})
	return nil
}
func (a *App) oauthTokenEndpoint(w http.ResponseWriter, r *http.Request) error {
	if !a.allowAttempt("oauth-token:" + sourceIP(r)) {
		return oauthError(w, "temporarily_unavailable", 429)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		return oauthError(w, "invalid_request", 400)
	}
	for _, v := range r.PostForm {
		if len(v) != 1 {
			return oauthError(w, "invalid_request", 400)
		}
	}
	client, e := a.oauthRequestClient(r)
	if e != nil {
		return oauthError(w, "invalid_client", 401)
	}
	grantType := r.PostForm.Get("grant_type")
	var device identity.NativeDeviceInfo
	if client.Native() && grantType == "authorization_code" {
		device = identity.NativeDeviceInfo{Name: r.PostForm.Get("device_name"), OS: r.PostForm.Get("device_os"), Arch: r.PostForm.Get("device_arch"), ClientVersion: r.PostForm.Get("client_version")}
		if device.Normalize() != nil {
			return oauthError(w, "invalid_request", 400)
		}
	}
	if client.Native() && grantType == "client_credentials" {
		return oauthError(w, "unauthorized_client", 400)
	}
	var grant identity.OAuthGrant
	nonce := ""
	if grantType == "authorization_code" {
		var redirect, challenge string
		grant, redirect, challenge, nonce, e = a.identity.ConsumeOAuthCode(r.Context(), client.ID, r.PostForm.Get("code"))
		if e != nil {
			return oauthError(w, "invalid_grant", 400)
		}
		verifier := r.PostForm.Get("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		actual := base64.RawURLEncoding.EncodeToString(sum[:])
		if redirect != r.PostForm.Get("redirect_uri") || !oauthVerifierPattern.MatchString(verifier) || subtle.ConstantTimeCompare([]byte(challenge), []byte(actual)) != 1 {
			return oauthError(w, "invalid_grant", 400)
		}
	} else if grantType != "refresh_token" && grantType != "client_credentials" {
		return oauthError(w, "unsupported_grant_type", 400)
	}
	var out map[string]any
	invalid := false
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		if grantType == "refresh_token" {
			var replay bool
			var e error
			grant, replay, e = a.identity.OAuthToken(ctx, r.PostForm.Get("refresh_token"), "refresh", client.ID, true)
			if e != nil {
				invalid = true
				return nil
			}
			if replay {
				invalid = true
				return a.audit.Detail(ctx, "", "identity.oauth_refresh_reuse", requestID(r), sourceIP(r), map[string]string{"clientId": client.ID})
			}
			if requested := strings.Fields(r.PostForm.Get("scope")); len(requested) > 0 {
				for _, scope := range requested {
					if !slices.Contains(grant.Scopes, scope) {
						invalid = true
						return nil
					}
				}
				grant.Scopes = requested
			}
		}
		if grantType == "client_credentials" {
			server, e := a.identity.OAuthSettings(ctx)
			if e != nil {
				return e
			}
			state, e := a.system.State(ctx)
			if e != nil {
				return e
			}
			if r.PostForm.Get("scope") != "service" {
				invalid = true
				return nil
			}
			grant = identity.OAuthGrant{ClientID: client.ID, ClientVersion: client.Version, ServerVersion: server.Version, AuthEpoch: state.AuthEpoch, Scopes: []string{"service"}}
		}
		c, server, e := a.identity.ValidateOAuthGrant(ctx, grant)
		if e != nil {
			invalid = true
			return nil
		}
		if grantType != "refresh_token" {
			grant, e = a.identity.CreateOAuthFamily(ctx, grant)
			if e != nil {
				return e
			}
		}
		access, refresh, expires, e := a.identity.IssueOAuthTokens(ctx, grant, grant.UserID != "" && slices.Contains(grant.Scopes, "offline_access"))
		if e != nil {
			return e
		}
		if c.Native() && grantType == "authorization_code" {
			if e = a.identity.DescribeNativeDevice(ctx, grant, device, sourceIP(r)); e != nil {
				return e
			}
		}
		out = map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": expires, "scope": strings.Join(grant.Scopes, " ")}
		if c.Native() {
			out["device_id"] = grant.ID
		}
		if refresh != "" {
			out["refresh_token"] = refresh
		}
		if grant.UserID != "" && slices.Contains(grant.Scopes, "openid") {
			claims, e := a.identity.OAuthClaims(ctx, c, grant)
			if e != nil {
				return e
			}
			sum := sha256.Sum256([]byte(access))
			claims["at_hash"] = base64.RawURLEncoding.EncodeToString(sum[:16])
			idToken, e := a.identity.SignOAuthIDToken(ctx, server.Issuer, client.ID, nonce, claims)
			if e != nil {
				return e
			}
			out["id_token"] = idToken
		}
		return a.audit.Detail(ctx, grant.UserID, "identity.oauth_token_issued", requestID(r), sourceIP(r), map[string]string{"clientId": client.ID, "grantType": grantType})
	})
	if e != nil {
		return e
	}
	if invalid {
		return oauthError(w, "invalid_grant", 400)
	}
	writeJSON(w, 200, out)
	return nil
}
func (a *App) oauthUserinfo(w http.ResponseWriter, r *http.Request) error {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return oauthError(w, "invalid_token", 401)
	}
	var claims map[string]any
	e := a.db.Within(r.Context(), func(ctx context.Context) error {
		g, _, e := a.identity.OAuthToken(ctx, strings.TrimPrefix(header, "Bearer "), "access", "", false)
		if e != nil {
			return e
		}
		if g.UserID == "" || !slices.Contains(g.Scopes, "openid") {
			return fault.Proof
		}
		c, _, e := a.identity.ValidateOAuthGrant(ctx, g)
		if e != nil {
			return e
		}
		claims, e = a.identity.OAuthClaims(ctx, c, g)
		return e
	})
	if e != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		return oauthError(w, "invalid_token", 401)
	}
	writeJSON(w, 200, claims)
	return nil
}
func (a *App) oauthTokenControl(w http.ResponseWriter, r *http.Request) error {
	if !a.allowAttempt("oauth-control:" + sourceIP(r)) {
		return oauthError(w, "temporarily_unavailable", 429)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		return oauthError(w, "invalid_request", 400)
	}
	for _, v := range r.PostForm {
		if len(v) != 1 {
			return oauthError(w, "invalid_request", 400)
		}
	}
	c, e := a.oauthRequestClient(r)
	if e != nil {
		return oauthError(w, "invalid_client", 401)
	}
	if c.Native() && !strings.HasSuffix(r.URL.Path, "/revoke") {
		return oauthError(w, "unauthorized_client", 403)
	}
	raw := r.PostForm.Get("token")
	if strings.HasSuffix(r.URL.Path, "/revoke") {
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.identity.RevokeOAuthToken(ctx, c.ID, raw); e != nil {
				return e
			}
			return a.audit.Detail(ctx, "", "identity.oauth_token_revoked", requestID(r), sourceIP(r), map[string]string{"clientId": c.ID})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(200)
		return nil
	}
	active := false
	out := map[string]any{"active": false}
	e = a.db.Within(r.Context(), func(ctx context.Context) error {
		g, _, e := a.identity.OAuthToken(ctx, raw, "access", c.ID, false)
		if e != nil {
			return nil
		}
		if _, _, e = a.identity.ValidateOAuthGrant(ctx, g); e != nil {
			return nil
		}
		active = true
		out["client_id"] = g.ClientID
		out["scope"] = strings.Join(g.Scopes, " ")
		if g.UserID != "" {
			out["sub"] = g.UserID
		}
		return nil
	})
	if e != nil {
		return e
	}
	out["active"] = active
	writeJSON(w, 200, out)
	return nil
}
func (a *App) oauthConsents(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	if r.Method == "GET" {
		items, e := a.identity.OAuthConsents(r.Context(), u.ID)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return nil
	}
	if !resource.ValidID(r.PathValue("id")) {
		return fault.Invalid
	}
	var proof backupProof
	if e = decode(w, r, &proof); e != nil {
		return e
	}
	e = a.withPasswordProof(r, proof, "", func(ctx context.Context, current *identity.User) error {
		if e := a.identity.RevokeOAuthConsent(ctx, current.ID, r.PathValue("id")); e != nil {
			return e
		}
		return a.audit.Detail(ctx, current.ID, "identity.oauth_consent_revoked", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
	})
	if e != nil {
		return e
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

// Public native clients prove possession with S256, never a shipped secret.
func (a *App) oauthRequestClient(r *http.Request) (identity.OAuthClient, error) {
	if r.PostForm.Has("client_secret") {
		return identity.OAuthClient{}, fault.Proof
	}
	id, secret, basic := r.BasicAuth()
	if !basic {
		if r.Header.Get("Authorization") != "" || r.PostForm.Get("client_id") != identity.NativeClientID {
			return identity.OAuthClient{}, fault.Proof
		}
		c, e := a.identity.OAuthClient(r.Context(), identity.NativeClientUUID, false)
		if e != nil || !c.Enabled {
			return identity.OAuthClient{}, fault.Proof
		}
		return c, nil
	}
	if !resource.ValidID(id) || (r.PostForm.Has("client_id") && r.PostForm.Get("client_id") != id) {
		return identity.OAuthClient{}, fault.Proof
	}
	return a.identity.OAuthClientAuthentication(r.Context(), id, secret)
}
