package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/ai"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/databaseauth"
	"next-terminal/backend/internal/modules/entitlements"
	"next-terminal/backend/internal/modules/files"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/buildmeta"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/listener"
	"next-terminal/backend/internal/platform/postgres"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const CookieName = "nt_session"
const Release = buildmeta.Release
const CapabilitiesRevision = buildmeta.CapabilitiesRevision

type Config struct {
	SecureCookies       bool
	EntitlementLocal    entitlements.CapabilityProvider
	EntitlementVerifier *entitlements.Verifier
	NativeSSHAddr       string
	NativeSSHName       string
	PasskeyRPID         string
	PasskeyOrigins      []string
	DatabaseProxyAddr   string
	PostgresProxyAddr   string
	DatabaseProxyName   string
	MP4URL              string
	MP4Token            string
	MP4Directory        string
	RestoreDirectory    string
	RestoreID           string
	StartFrozen         bool
	BackupDirectory     string
	DatabaseURL         string
	WebsitePublicPort   int
	RDPDriveEnabled     bool
	GuacdAddr           string
	GuacdRelayHost      string
	AgentBinaryPath     string
	AllowedOrigins      []string
	AllowRequestOrigin  bool
	EncryptionKey       []byte
	VaultKey            []byte
	CaptchaRequired     bool
}
type App struct {
	entitlements             *entitlements.Service
	nativeSSHReady           atomic.Bool
	nativeRDPAddress         atomic.Pointer[string]
	managedRDP               *NativeRDPOptions
	managedRDPSlots          chan struct{}
	oidcHTTPClient           *http.Client // nil in production; isolated trusted-CA fixtures in tests
	databaseProxyReady       atomic.Bool
	postgresProxyReady       atomic.Bool
	databaseProxyCertificate atomic.Value
	databasePasswords        *databaseauth.Verifier
	maintenance              atomic.Bool
	requests                 sync.RWMutex
	backgroundMu             sync.Mutex
	backgroundParent         context.Context
	backgroundCancel         context.CancelFunc
	backgroundDone           chan struct{}
	db                       *postgres.DB
	identity                 *identity.Service
	system                   *system.Service
	audit                    *audit.Service
	authz                    *authorization.Service
	config                   Config
	hashSlots                chan struct{}
	aiSlots                  chan struct{}
	probeSlots               chan struct{}
	resources                *resource.Service
	vault                    *vault.Service
	connectivity             *connectivity.Service
	sessions                 *session.Service
	files                    *files.Service
	fileSlots                chan struct{}
	liveMu                   sync.Mutex
	live                     map[string]*liveSession
	mu                       sync.Mutex
	configurationMu          sync.Mutex
	listenerManager          *listener.Manager
	publicListeners          *listener.Manager
	publicHTTPAddress        string
	publicWebsiteAddress     string
	publicHTTPHandler        http.Handler
	publicWebsiteHandler     http.Handler
	configurationNode        string
	attempts                 map[string]attempt
}
type attempt struct {
	count int
	until time.Time
}

func New(pool *pgxpool.Pool, c Config) *App {
	if c.DatabaseProxyName == "" {
		c.DatabaseProxyName = "localhost"
	}
	db := postgres.New(pool)
	if len(c.EncryptionKey) != 32 || len(c.VaultKey) != 32 {
		panic("32-byte MFA encryption key required")
	}
	a := &App{databasePasswords: databaseauth.NewVerifier(), db: db, identity: identity.New(db, c.EncryptionKey), system: system.New(db), audit: audit.New(db), authz: authorization.New(db), config: c, resources: resource.New(db), vault: vault.New(db, c.VaultKey), connectivity: connectivity.New(db), sessions: session.New(db), files: files.New(db), fileSlots: make(chan struct{}, 4), live: map[string]*liveSession{}, probeSlots: make(chan struct{}, 4), hashSlots: make(chan struct{}, 4), attempts: map[string]attempt{}}
	if c.StartFrozen {
		a.maintenance.Store(true)
	}
	a.aiSlots = make(chan struct{}, 4)
	a.entitlements = entitlements.NewService(db, c.EntitlementLocal, c.EntitlementVerifier)
	db.SetAdmissionCheck(a.entitlements.Admit)
	a.configurationNode = identity.NewToken()[:24]
	return a
}

type apiError struct {
	status          int
	reason, message string
}

func (e *apiError) Error() string                      { return e.reason }
func problem(status int, reason, message string) error { return &apiError{status, reason, message} }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func token(r *http.Request) string {
	c, e := r.Cookie(CookieName)
	if e != nil {
		return ""
	}
	return c.Value
}
func sourceIP(r *http.Request) string {
	if ip, ok := r.Context().Value(trustedIPKey{}).(string); ok {
		return ip
	}
	ip, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return ip
}
func requestID(r *http.Request) string { return r.Header.Get("X-Internal-Request-ID") }
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		return problem(415, "json_required", "请求必须使用 JSON")
	}
	limit := int64(16 * 1024)
	if r.URL.Path == "/api/settings" || r.URL.Path == "/api/admin/properties" {
		limit = 256 * 1024
	}
	if r.URL.Path == "/api/admin/entitlements" {
		limit = 48 * 1024
	}
	if strings.HasPrefix(r.URL.Path, "/api/ai/") || r.URL.Path == "/api/admin/ai/settings" {
		limit = 64 * 1024
	}
	if r.URL.Path == "/api/settings" || r.URL.Path == "/api/admin/properties" || r.URL.Path == "/api/admin/ai/settings" {
		limit = 512 * 1024
	}
	if strings.HasPrefix(r.URL.Path, "/api/database-work-orders") {
		limit = 2 << 20
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/certificates") || strings.HasPrefix(r.URL.Path, "/api/admin/websites") || strings.HasPrefix(r.URL.Path, "/api/admin/notifications") {
		limit = 1024 * 1024
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/credentials") || strings.HasPrefix(r.URL.Path, "/api/admin/database-assets") {
		limit = 64 * 1024
	}
	if r.URL.Path == "/api/admin/assets/import" {
		limit = 1024 * 1024
	}
	if strings.HasPrefix(r.URL.Path, "/api/storage/files/") && strings.HasSuffix(r.URL.Path, "/text") {
		limit = 8 << 20
	}
	if r.URL.Path == "/api/admin/geodata" {
		limit = 32 << 20
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/policies/") {
		limit = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return problem(400, "invalid_body", "请求格式或字段无效")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return problem(400, "invalid_body", "只允许一个 JSON 对象")
	}
	return nil
}
func (a *App) allowAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.attempts {
		if now.After(v.until) {
			delete(a.attempts, k)
		}
	}
	v, ok := a.attempts[ip]
	if !ok {
		if len(a.attempts) >= 4096 {
			return false
		}
		v = attempt{until: now.Add(time.Minute)}
	}
	v.count++
	a.attempts[ip] = v
	return v.count <= 20
}
func (a *App) authenticated(r *http.Request) (*identity.User, error) {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return a.apiKeyIdentity(r, strings.TrimPrefix(header, "Bearer "))
	}
	state, e := a.system.State(r.Context())
	if e != nil {
		return nil, e
	}
	if !state.Initialized {
		return nil, problem(418, "setup_required", "请先初始化管理员")
	}
	u, e := a.identity.Current(r.Context(), token(r))
	if e != nil {
		return nil, e
	}
	if u == nil {
		return nil, problem(401, "unauthenticated", "请先登录")
	}
	return u, nil
}
func (a *App) Handler() http.Handler {
	return a.handler(nil)
}

func (a *App) handler(registered func(string)) http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		if registered != nil {
			registered(pattern)
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			control := r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || (r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/admin/backup/"))
			if !control {
				if !a.enterRequest() {
					a.failure(w, r, problem(503, "maintenance", "系统正在备份或恢复维护，请稍后重试"))
					return
				}
				defer a.requests.RUnlock()
			}
			if e := fn(w, r); e != nil {
				a.failure(w, r, e)
			}
		})
	}
	handle("GET /healthz", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return nil
	})
	handle("GET /readyz", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.system.State(r.Context()); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"status": "ready", "restoreId": a.config.RestoreID, "frozen": a.maintenance.Load()})
		return nil
	})
	handle("GET /api/branding", func(w http.ResponseWriter, r *http.Request) error {
		settings, e := a.system.Settings(r.Context())
		if e != nil {
			return e
		}
		w.Header().Set("Cache-Control", "no-store")
		logo := "/api/logo?v=puppeteer-mascot-2"
		if id, ok := settings["system-logo"].(string); ok && id != "" {
			logo = "/api/logo?v=" + id
		}
		writeJSON(w, 200, map[string]any{"name": settings["system-name"], "version": Release, "copyright": settings["system-copyright"], "icp": settings["system-icp"], "logo": logo, "debug": true})
		return nil
	})
	handle("GET /api/logo", func(w http.ResponseWriter, r *http.Request) error {
		configuration, err := a.system.Configuration(r.Context(), 0)
		if err != nil {
			return err
		}
		if id := configuration.Values.String("system-logo"); id != "" {
			data, err := a.system.Logo(r.Context(), id)
			if err != nil {
				return err
			}
			serveBrandLogo(w, r, data)
			return nil
		}
		serveBrandLogo(w, r, defaultBrandLogo)
		return nil
	})
	handle("GET /api/setup-status", func(w http.ResponseWriter, r *http.Request) error {
		s, e := a.system.State(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"needSetup": !s.Initialized})
		return nil
	})
	handle("POST /api/setup-user", a.setup)
	handle("GET /api/login-status", a.loginStatus)
	handle("POST /api/login", a.login)
	handle("POST /api/logout", a.logout)
	a.routesR02(handle)
	a.routesR03(handle)
	a.routesR04(handle)
	a.routesR05(handle)
	a.routesR06(handle)
	a.routesR07(handle)
	a.routesR08Policies(handle)
	a.routesR08Approval(handle)
	a.routesR08Notifications(handle)
	a.routesR09PKI(handle)
	a.routesR09Websites(handle)
	a.routesR09WebsiteRecords(handle)
	a.routesR09ACME(handle)
	a.routesR10Snippets(handle)
	a.routesR10Tasks(handle)
	a.routesR10Rotation(handle)
	a.routesAccessRetention(handle)
	a.routesR11Backup(handle)
	a.routesStorage(handle)
	handle("GET /api/account/info", a.accountInfo)
	a.registerAI(handle)
	handle("GET /api/admin/oidc-providers", a.oidcProviders)
	handle("GET /api/ldap/login/providers", a.ldapLoginProviders)
	handle("POST /api/ldap/login", a.ldapLogin)
	handle("GET /api/account/ldap-identities", a.ldapBindings)
	handle("POST /api/account/ldap-providers/{id}/bind", a.bindLDAP)
	handle("DELETE /api/account/ldap-identities/{id}", a.unbindLDAP)
	handle("GET /.well-known/openid-configuration", a.oauthDiscovery)
	handle("GET /api/oidc/server/.well-known/openid-configuration", a.oauthDiscovery)
	handle("GET /api/oidc/server/jwks", a.oauthJWKS)
	handle("GET /api/oidc/server/authorize", a.oauthAuthorize)
	handle("GET /api/oidc/server/consent", a.oauthConsent)
	handle("POST /api/oidc/server/consent", a.oauthConsent)
	handle("POST /api/oidc/server/token", a.oauthTokenEndpoint)
	handle("GET /api/oidc/server/userinfo", a.oauthUserinfo)
	handle("POST /api/oidc/server/revoke", a.oauthTokenControl)
	handle("POST /api/oidc/server/introspect", a.oauthTokenControl)
	handle("GET /api/admin/login-policy", a.loginPolicy)
	handle("PUT /api/admin/login-policy", a.loginPolicy)
	handle("GET /api/admin/contact-settings", a.contactSettings)
	handle("PUT /api/admin/contact-settings", a.contactSettings)
	handle("GET /api/account/contacts", a.contacts)
	handle("DELETE /api/account/contacts/{id}", a.contacts)
	handle("POST /api/account/contacts/start", a.startContact)
	handle("POST /api/account/contacts/finish", a.finishContact)
	handle("POST /api/contact/start", a.startContactLogin)
	handle("POST /api/contact/finish", a.finishContactLogin)
	handle("GET /api/admin/oauth-server", a.oauthServerSettings)
	handle("PUT /api/admin/oauth-server", a.oauthServerSettings)
	handle("GET /api/admin/oauth-clients", a.oauthClients)
	handle("POST /api/admin/oauth-clients", a.saveOAuthClient)
	handle("PUT /api/admin/oauth-clients/{id}", a.saveOAuthClient)
	handle("GET /api/account/oauth-consents", a.oauthConsents)
	handle("DELETE /api/account/oauth-consents/{id}", a.oauthConsents)
	handle("GET /api/account/native-devices", a.nativeDevices)
	handle("DELETE /api/account/native-devices/{id}", a.nativeDevices)
	handle("GET /api/admin/ldap-providers", a.ldapProviders)
	handle("POST /api/admin/ldap-providers", a.saveLDAPProvider)
	handle("PUT /api/admin/ldap-providers/{id}", a.saveLDAPProvider)
	handle("POST /api/admin/ldap-providers/{id}/test", a.testLDAPProvider)
	handle("POST /api/admin/ldap-providers/{id}/sync-preview", a.previewLDAPSync)
	handle("POST /api/admin/ldap-sync", a.applyLDAPSync)
	handle("POST /api/admin/oidc-providers/{id}/test", a.testOIDCProvider)
	handle("POST /api/admin/oidc-providers/{id}/authorize-test", a.startOIDCAuthorizationTest)
	handle("GET /api/wechat-work/callback", a.finishOIDCAuthorizationTest)
	handle("GET /api/oidc/callback", a.finishOIDCAuthorizationTest)
	handle("GET /api/oidc/login/providers", a.oidcLoginProviders)
	handle("POST /api/oidc/login/{id}/start", a.startOIDCLogin)
	handle("GET /api/account/oidc-identities", a.listOIDCBindings)
	handle("POST /api/account/oidc-providers/{id}/bind", a.startOIDCBinding)
	handle("DELETE /api/account/oidc-identities/{id}", a.unbindOIDCIdentity)
	handle("POST /api/admin/oidc-providers", a.saveOIDCProvider)
	handle("PUT /api/admin/oidc-providers/{id}", a.saveOIDCProvider)
	handle("GET /api/account/passkeys", a.listPasskeys)
	handle("POST /api/passkey/login/start", a.startPasskeyLogin)
	handle("POST /api/passkey/login/finish", a.finishPasskeyLogin)
	handle("POST /api/account/passkeys/start", a.startPasskeyRegistration)
	handle("POST /api/account/passkeys/finish", a.finishPasskeyRegistration)
	handle("DELETE /api/account/passkeys/{id}", a.revokePasskey)
	handle("GET /api/account/certificates", a.personalCertificates)
	handle("POST /api/account/certificates/{id}/p12", a.personalCertificateAction(true))
	handle("POST /api/account/certificates/{id}/revoke", a.personalCertificateAction(false))
	for _, kind := range []string{"api", "ssh"} {
		path := "/api/account/" + kind + "-keys"
		handle("GET "+path, a.personalCredentials(kind))
		handle("POST "+path, a.changePersonalCredential(kind, false))
		handle("DELETE "+path+"/{id}", a.changePersonalCredential(kind, true))
	}
	handle("GET /api/account/database-password", a.databasePasswordStatus)
	a.registerDatabaseWorkOrders(handle)
	a.registerDatabaseViews(handle)
	a.registerConfiguration(handle)
	a.routesR16Monitoring(handle)
	a.routesR16Diagnostics(handle)
	a.routesR16BrandLogo(handle)
	handle("GET /api/admin/database-assets", a.databaseAssets(false))
	handle("GET /api/portal/database-assets", a.databaseAssets(true))
	handle("POST /api/admin/database-assets", a.saveDatabaseAsset)
	handle("POST /api/admin/database-assets/{id}/test", a.testDatabaseAsset)
	handle("PUT /api/admin/database-assets/{id}", a.saveDatabaseAsset)
	handle("DELETE /api/admin/database-assets/{id}", a.deleteDatabaseAsset)
	handle("POST /api/account/database-password", a.changeDatabasePassword(false))
	handle("GET /api/account/password", a.accountPassword)
	handle("POST /api/account/password", a.accountPassword)
	handle("DELETE /api/account/database-password", a.changeDatabasePassword(true))
	handle("GET /api/license", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.authenticated(r); e != nil {
			return e
		}
		v, _, e := a.entitlements.Snapshot(r.Context())
		if e != nil && !errors.Is(e, entitlements.ErrInvalid) && !errors.Is(e, entitlements.ErrExpired) && !errors.Is(e, entitlements.ErrNotYetValid) {
			return e
		}
		writeJSON(w, 200, map[string]any{"type": "free", "source": v.Source, "state": v.State, "oem": false})
		return nil
	})
	handle("GET /api/admin/entitlements", a.entitlementStatus)
	handle("POST /api/admin/entitlements", a.importEntitlement)
	handle("GET /api/native/v1/assets", a.nativeAssets)
	handle("GET /api/native/ssh-gateway", a.nativeSSHInfo)
	handle("GET /api/admin/native-forward", a.nativeForwardConfig)
	handle("PUT /api/admin/native-forward", a.nativeForwardConfig)
	handle("GET /api/sessions/{id}/forward-streams", a.nativeForwardDetails)
	handle("GET /api/native/ssh-launch/{id}", a.nativeSSHLaunch)
	handle("POST /api/native/v1/connect", a.nativeConnect)
	handle("POST /api/native/v1/rdp-file", a.nativeRDPFile)
	handle("GET /api/native/v1/bridge", a.nativeBridge)
	handle("DELETE /api/native/v1/sessions/{id}", a.nativeTerminate)
	handle("GET /api/system/runtime", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.authenticated(r); e != nil {
			return e
		}
		s, e := a.system.State(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"backendId": "nt-go", "instanceId": s.InstanceID, "release": Release, "contractVersion": 1, "schemaVersion": s.SchemaVersion, "capabilitiesRevision": CapabilitiesRevision, "round": "R16", "environment": "development"})
		return nil
	})
	handle("GET /api/system/capabilities", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.authenticated(r); e != nil {
			return e
		}
		caps := Capabilities()
		if a.config.GuacdAddr != "" && a.config.GuacdRelayHost != "" {
			caps["features"].(map[string]any)["session.graphics"] = map[string]any{"state": "available", "reason": nil}
		}
		caps["protocols"] = a.protocolCapabilities()
		entitlement, _, err := a.entitlements.Snapshot(r.Context())
		if err != nil && !errors.Is(err, entitlements.ErrInvalid) && !errors.Is(err, entitlements.ErrExpired) && !errors.Is(err, entitlements.ErrNotYetValid) {
			return err
		}
		applyEntitlementCapabilities(caps, entitlement)
		writeJSON(w, 200, caps)
		return nil
	})
	handle("GET /api/system/audit/recent", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		if e := a.allowed(r.Context(), u, "audit.read"); e != nil {
			return e
		}
		events, e := a.audit.Recent(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, events)
		return nil
	})
	handle("GET /api/admin/properties", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.authenticated(r)
		if e != nil {
			return e
		}
		if e := a.allowed(r.Context(), u, "settings.read"); e != nil {
			return e
		}
		var v map[string]any
		var revision int64
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if err := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&revision); err != nil {
				return err
			}
			var err error
			v, err = a.system.Settings(ctx)
			return err
		})
		if e != nil {
			return e
		}
		w.Header().Set("ETag", configTag(revision))
		w.Header().Set("X-Settings-Revision", fmt.Sprint(revision))
		writeJSON(w, 200, v)
		return nil
	})
	handle("/api/", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.authenticated(r); e != nil {
			return e
		}
		return problem(501, "not_implemented", "此功能尚未开放；当前版本为 "+Release)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		id := identity.NewToken()[:24]
		r.Header.Set("X-Internal-Request-ID", id)
		w.Header().Set("X-Request-ID", id)
		// Only credential-based native enrollment is exempt from browser Origin checks.
		nativeEnroll := r.Method == "POST" && (r.URL.Path == "/api/agent/v1/enroll" || r.URL.Path == "/api/agent/v1/rotate") && r.Header.Get("Origin") == "" && token(r) == ""
		nativeOAuth := r.Method == "POST" && (r.URL.Path == "/api/oidc/server/token" || r.URL.Path == "/api/oidc/server/revoke" || r.URL.Path == "/api/oidc/server/introspect") && r.Header.Get("Origin") == "" && token(r) == ""
		nativeSession := ((r.Method == "POST" && (r.URL.Path == "/api/native/v1/connect" || r.URL.Path == "/api/native/v1/rdp-file" || r.URL.Path == "/api/native/v1/access-mfa")) || (r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/native/v1/sessions/"))) && r.Header.Get("Origin") == "" && token(r) == "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Method != "GET" && r.Method != "HEAD" && !nativeEnroll && !nativeOAuth && !nativeSession {
			allowed := a.allowsBrowserOrigin(r)
			if !allowed {
				a.failure(w, r, problem(403, "origin_denied", "请求来源未授权"))
				return
			}
		}
		timeout := 15 * time.Second
		if r.URL.Path == "/api/ai/ws" || r.URL.Path == "/api/agent/v1/connect" || r.URL.Path == "/api/native/v1/bridge" || (strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/ws")) {
			timeout = 4 * time.Hour
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			resolved, e := a.resolveAPIClient(r)
			if e != nil {
				a.failure(w, r, e)
				return
			}
			r = resolved
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) failure(w http.ResponseWriter, r *http.Request, e error) {
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		switch pg.Code {
		case "23505", "23503", "23001":
			e = problem(409, "conflict", "名称已存在或记录仍被引用")
		case "22P02":
			e = fault.Invalid
		}
	}
	switch {
	case errors.Is(e, session.ErrAccessMFA):
		e = problem(403, "access_mfa_required", "连接远程资产前请完成二次验证")
	case errors.Is(e, system.ErrRetentionPreviewRequired):
		e = problem(409, "retention_preview_required", "缩短保留期限前，请按当前配置版本生成清理预览（十分钟内有效）")
	case errors.Is(e, identity.ErrSessionLimit):
		e = problem(409, "login_session_limit", "已达到账号登录会话上限，请先退出其他浏览器会话或联系管理员")
	case errors.Is(e, fault.Invalid):
		e = problem(422, "invalid_input", "字段无效，请检查名称、关系和权限")
	case errors.Is(e, ai.ErrDailyBudget):
		e = problem(409, "ai_daily_budget", "当日预算不足，未发送模型请求。请等待 UTC 次日或联系管理员调整预算。")
	case errors.Is(e, ai.ErrTurnBudget):
		e = problem(409, "ai_turn_budget", "本轮保守预留超过单轮费用上限，未发送模型请求。请缩短上下文、减少输出额度或调整预算。")
	case errors.Is(e, ai.ErrPricing):
		e = problem(422, "ai_pricing_required", "启用预算前需配置模型输入与输出单价。")
	case errors.Is(e, fault.Conflict):
		e = problem(409, "conflict", "记录已变化、仍被引用，或操作会移除最后一个管理员，请刷新")
	case errors.Is(e, fault.Denied):
		e = problem(403, "permission_denied", "当前账号没有此操作权限")
	case errors.Is(e, entitlements.ErrQuota):
		e = problem(409, "quota_exceeded", "已达到许可配额，请释放名额或联系管理员调整许可")
	case errors.Is(e, entitlements.ErrDenied):
		e = problem(403, "not_entitled", "当前部署许可未启用此功能")
	case errors.Is(e, entitlements.ErrExpired):
		e = problem(403, "license_expired", "许可已过期，请联系管理员更新许可")
	case errors.Is(e, entitlements.ErrInvalid), errors.Is(e, entitlements.ErrNotYetValid):
		e = problem(422, "invalid_license", "许可缺失、无效、尚未生效或不属于本实例")
	case errors.Is(e, fault.Proof):
		e = problem(400, "invalid_proof", "验证信息错误、过期或已使用，请重试")
	case errors.Is(e, fault.Locked):
		e = problem(429, "account_locked", "账号或来源 IP 暂时锁定，请稍后重试或联系管理员")
	case errors.Is(e, pgx.ErrNoRows):
		e = problem(404, "not_found", "记录不存在")
	}
	var api *apiError
	if !errors.As(e, &api) {
		slog.Error("request failed", "requestId", requestID(r), "path", r.URL.Path)
		api = &apiError{503, "service_unavailable", "服务暂不可用，请稍后重试"}
	}
	writeJSON(w, api.status, map[string]any{"code": 0, "reason": api.reason, "message": api.message, "requestId": requestID(r)})
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)

func (a *App) setup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		Confirm   string `json:"newPassword2"`
		Type      string `json:"type"`
		Recording string `json:"recording"`
		Watermark string `json:"watermark"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !usernamePattern.MatchString(in.Username) || utf8.RuneCountInString(in.Password) < 12 || len(in.Password) > 1024 || in.Password != in.Confirm {
		return problem(422, "invalid_setup", "账号需3–64位字母、数字或_.-；密码至少12字符且两次一致")
	}
	err := a.db.Within(r.Context(), func(ctx context.Context) error {
		done, e := a.system.LockSetup(ctx)
		if e != nil {
			return e
		}
		if done {
			return problem(409, "already_initialized", "实例已初始化，不能重复创建管理员")
		}
		if e = a.validateNewPassword(ctx, in.Username, in.Password); e != nil {
			return e
		}
		select {
		case a.hashSlots <- struct{}{}:
			defer func() { <-a.hashSlots }()
		default:
			return problem(429, "busy", "请求过多，请稍后重试")
		}
		u, e := a.identity.CreateAdmin(ctx, strings.ToLower(in.Username), in.Username, identity.HashPassword(in.Password))
		if e != nil {
			return e
		}
		if e = a.system.Initialize(ctx); e != nil {
			return e
		}
		return a.audit.Operation(ctx, u.ID, "setup", "succeeded", requestID(r), sourceIP(r))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	u, e := a.identity.Session(r.Context(), token(r))
	if e != nil {
		return e
	}
	if u != nil {
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.identity.Revoke(ctx, token(r)); e != nil {
				return e
			}
			return a.audit.Operation(ctx, u.ID, "logout", "succeeded", requestID(r), sourceIP(r))
		})
		if e != nil {
			return e
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(204)
	return nil
}
func Capabilities() map[string]any {
	features := map[string]any{}
	for _, key := range []string{"native.devices", "native.ssh-gateway", "native.ssh-launch", "native.ssh-forward", "native.https-forward", "native.ssh", "native.identity", "native.assets", "identity.federation", "identity.wecom", "identity.oidc-server", "identity.contacts", "identity.ldap-sync", "identity.login-policy"} {
		features[key] = map[string]any{"state": "available", "reason": nil}
	}
	features["identity.oidc-login"] = map[string]any{"state": "available", "reason": nil}
	features["ai.chat"] = map[string]any{"state": "available", "reason": nil}
	features["ai.inference"] = map[string]any{"state": "available", "reason": nil}
	features["ai.models"] = map[string]any{"state": "available", "reason": nil}
	features["ai.profiles"] = map[string]any{"state": "available", "reason": nil}
	features["ai.stream"] = map[string]any{"state": "available", "reason": nil}
	for _, key := range []string{"ai.assistant", "ai.queue", "ai.tools", "ai.budget", "ai.history", "ai.protocols"} {
		features[key] = map[string]any{"state": "available", "reason": nil}
	}
	features["ai.readonly_context"] = map[string]any{"state": "available", "reason": nil}
	features["identity.oidc-config"] = map[string]any{"state": "available", "reason": nil}
	features["identity.ldap-login"] = map[string]any{"state": "available", "reason": nil}
	features["identity.ldap-config"] = map[string]any{"state": "available", "reason": nil}
	features["automation.snippets"] = map[string]any{"state": "available", "reason": nil}
	features["automation.jobs"] = map[string]any{"state": "available", "reason": nil}
	for _, key := range []string{"automation.execute", "notification.send", "files.write"} {
		features[key] = map[string]any{"state": "available", "reason": nil}
	}
	features["vault.rotation"] = map[string]any{"state": "available", "reason": nil}
	features["audit.retention"] = map[string]any{"state": "available", "reason": nil}
	features["audit.recording.download"] = map[string]any{"state": "available", "reason": nil}
	for _, key := range []string{"identity.client-certificates", "identity.api-keys", "identity.ssh-keys", "database.assets", "database.password", "database.mysql", "database.workorders", "database.audit"} {
		features[key] = map[string]any{"state": "available", "reason": nil}
	}
	for _, key := range []string{"backup.archive", "backup.restore", "backup.remote", "system.runtime", "identity.self", "system.settings.read", "audit.basic", "identity.users", "identity.mfa", "assets.hosts", "vault.credentials", "authorization.resources", "session.ssh", "files.sftp", "agent.gateway", "session.telnet", "session.observe", "storage.spaces", "storage.acl", "storage.text", "storage.audit", "storage.upload.tasks", "approval.access", "authorization.policies", "notification.delivery", "web.proxy", "certificates.pki"} {
		features[key] = map[string]any{"state": "available", "reason": nil}
	}
	features["database.postgres"] = map[string]any{"state": "available", "reason": nil, "profile": "readonly-preview"}
	for key, round := range map[string]string{"session.graphics": "R07", "session.rdp.native": "R14"} {
		features[key] = map[string]any{"state": "not_implemented", "reason": "planned_round_" + round}
	}
	features["system.settings.revisions"] = map[string]any{"state": "available", "scope": "login-password-mfa-listeners-mail-client-ip-postgres-branding-retention-resources-watermark"}
	features["session.watermark"] = map[string]any{"state": "available", "scope": "browser-display", "reason": "新会话快照；网页终端、图形桌面和观察画面，不烧录到录屏或原生客户端"}
	features["system.settings.history"] = map[string]any{"state": "available", "scope": "registered-non-secret-properties"}
	features["system.entitlements"] = map[string]any{"state": "available", "scope": "users-resources-host-database-sessions"}
	for _, key := range []string{"system.monitoring", "system.diagnostics", "audit.statistics", "system.branding", "system.settings.cli"} {
		features[key] = map[string]any{"state": "available", "reason": nil}
	}
	return map[string]any{"schemaVersion": 1, "revision": CapabilitiesRevision, "backendId": "nt-go", "features": features}
}
