package application

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/server"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/databaseauth"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/mysqlproxy"
	"strconv"
	"sync"
	"time"
)

func (a *App) databaseProxyTLS(ctx context.Context) (*tls.Config, error) {
	config, id, err := a.defaultDatabaseProxyTLS(ctx)
	if err == nil {
		a.databaseProxyCertificate.Store(id)
	}
	return config, err
}
func (a *App) defaultDatabaseProxyTLS(ctx context.Context) (*tls.Config, string, error) {
	var id string
	e := a.db.Within(ctx, func(c context.Context) error {
		if _, e := a.db.Exec(c, `SELECT pg_advisory_xact_lock(78201612)`); e != nil {
			return e
		}
		e := a.db.Row(c, `SELECT typed_value#>>'{}' FROM settings WHERE key='database-proxy-certificate-id'`).Scan(&id)
		if e == nil {
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		cert, key, e := pki.Generate("MySQL proxy TLS", []string{a.config.DatabaseProxyName}, 365, false, nil)
		if e != nil {
			return e
		}
		defer clear(key)
		id, e = a.certificates().Store(c, pki.Certificate{Name: "MySQL proxy TLS", Kind: "server", Source: "self_signed", PEM: string(cert)}, key)
		if e != nil {
			return e
		}
		_, e = a.db.Exec(c, `INSERT INTO settings(key,typed_value) VALUES('database-proxy-certificate-id',to_jsonb($1::text))`, id)
		return e
	})
	if e != nil {
		return nil, "", e
	}
	certificate, e := a.certificates().TLS(ctx, id)
	if e != nil {
		return nil, "", e
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, id, nil
}
func (a *App) RunDatabaseProxy(ctx context.Context) {
	if a.config.DatabaseProxyAddr == "" {
		return
	}
	for ctx.Err() == nil {
		e := a.serveDatabaseProxy(ctx)
		a.databaseProxyReady.Store(false)
		if ctx.Err() != nil {
			return
		}
		slog.Error("database proxy stopped", "error", e)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (a *App) serveDatabaseProxy(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tlsConfig, e := a.databaseProxyTLS(ctx)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp", a.config.DatabaseProxyAddr)
	if e != nil {
		return e
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	a.databaseProxyReady.Store(true)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	slots := make(chan struct{}, 64)
	for {
		raw, e := listener.Accept()
		if e != nil {
			return e
		}
		select {
		case slots <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		workers.Add(1)
		go func() { defer workers.Done(); defer func() { <-slots }(); a.databaseConnection(ctx, raw, tlsConfig) }()
	}
}
func (a *App) databaseAccess(ctx context.Context, username, alias string, passwordVersion int64, assetVersion int64) (identity.User, database.Asset, int64, error) {
	user, version, e := a.identity.DatabasePrincipal(ctx, username)
	if e != nil {
		return user, database.Asset{}, 0, fault.Denied
	}
	if passwordVersion != 0 && version != passwordVersion {
		return user, database.Asset{}, version, fault.Denied
	}
	asset, e := database.New(a.db).AssetByAlias(ctx, alias)
	if e != nil {
		return user, asset, version, fault.Denied
	}
	if assetVersion != 0 && asset.Version != assetVersion {
		return user, asset, version, fault.Denied
	}
	if user.Type != "admin" {
		departments, e := a.identity.DepartmentIDs(ctx, user.ID)
		if e != nil {
			return user, asset, version, e
		}
		allowed, e := a.authz.DatabaseAccess(ctx, user.ID, asset.ID, departments)
		if e != nil {
			return user, asset, version, e
		}
		if !allowed {
			return user, asset, version, fault.Denied
		}
	}
	return user, asset, version, nil
}
func (a *App) databaseConnection(parent context.Context, raw net.Conn, tlsConfig *tls.Config) {
	defer raw.Close()
	if !a.enterRequest() {
		return
	}
	defer a.requests.RUnlock()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	raw.SetDeadline(time.Now().Add(15 * time.Second))
	service := database.New(a.db)
	var username, alias, sessionID, queryID string
	var passwordVersion int64
	var nativeWrite bool
	var writeCredentialVersion int64
	var user identity.User
	var asset database.Asset
	var target *client.Conn
	defer func() {
		if target != nil {
			target.Close()
		}
		if sessionID != "" {
			cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if e := service.EndSession(cleanup, sessionID, "connection_closed"); e != nil {
				slog.Error("database session finalization failed", "session", sessionID)
			}
		}
	}()
	protocol := mysqlproxy.NewServer(tlsConfig, func(authCtx context.Context, wire, password string) error {
		var e error
		username, alias, e = databaseauth.SplitIdentity(wire)
		if e != nil {
			return e
		}
		user, asset, passwordVersion, e = a.databaseAccess(authCtx, username, alias, 0, 0)
		if e != nil {
			return e
		}
		if asset.Engine != "mysql" {
			return fault.Denied
		}
		verifier, e := a.vault.DatabaseVerifier(authCtx, user.ID, passwordVersion)
		if e != nil {
			return e
		}
		defer clear(verifier)
		return a.databasePasswords.Verify(authCtx, password, string(verifier))
	})
	handshake := &databaseHandshake{}
	downstream, e := protocol.NewCustomizedConn(raw, &mysqlproxy.IdentityHandler{Failure: func(*server.Conn, error) {
		c, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = a.audit.Operation(c, user.ID, "database.login", "denied", "mysql", raw.RemoteAddr().String())
	}, Ready: func(*server.Conn) error {
		var e error
		user, asset, _, e = a.databaseAccess(ctx, username, alias, passwordVersion, asset.Version)
		if e != nil {
			return e
		}
		if handshake.requested != "" && handshake.requested != asset.Database {
			return fault.Denied
		}
		sessionID, e = service.BeginSession(ctx, user.ID, asset, passwordVersion, raw.RemoteAddr().String())
		if e != nil {
			return e
		}
		security, err := a.system.SecurityConfiguration(ctx)
		if err != nil {
			return err
		}
		nativeWrite = !security.Values.Bool(system.DatabaseBlockDMLKey) && asset.NativeWriteCredentialID != "" && a.allowed(ctx, &user, "database.write") == nil
		credentialID := asset.ReadonlyCredentialID
		if nativeWrite {
			credentialID = asset.NativeWriteCredentialID
			if _, err := a.db.Exec(ctx, `UPDATE database_sessions SET access_profile='dml' WHERE id=$1`, sessionID); err != nil {
				return err
			}
		}
		var endpoint mysqlproxy.Target
		e = a.db.Within(ctx, func(c context.Context) error {
			credential, secret, e := a.vault.DatabaseAuth(c, credentialID, user.ID, sessionID)
			if e != nil {
				return e
			}
			if credential.Kind != "password" {
				return fault.Denied
			}
			writeCredentialVersion = credential.ActiveVersion
			endpoint = mysqlproxy.Target{Address: net.JoinHostPort(asset.Host, strconv.Itoa(asset.Port)), User: credential.Username, Password: secret.Password, Database: asset.Database, ServerName: asset.TLSServerName, CA: asset.TLSCA, Timeout: time.Duration(asset.ConnectTimeout) * time.Second}
			return nil
		})
		if e != nil {
			return e
		}
		if nativeWrite {
			target, e = mysqlproxy.ConnectDMLTarget(ctx, endpoint)
		} else {
			target, e = mysqlproxy.ConnectTarget(ctx, endpoint, true)
		}
		endpoint.Password = ""
		return e
	}}, handshake)
	if e != nil {
		return
	}
	defer downstream.Close()
	raw.SetDeadline(time.Time{})
	check := func(c context.Context) error {
		if a.maintenance.Load() {
			return fault.Denied
		}
		currentUser, _, _, e := a.databaseAccess(c, username, alias, passwordVersion, asset.Version)
		if e != nil {
			return e
		}
		if nativeWrite {
			security, err := a.system.SecurityConfiguration(c)
			if err != nil {
				return err
			}
			if security.Values.Bool(system.DatabaseBlockDMLKey) || a.allowed(c, &currentUser, "database.write") != nil {
				return fault.Denied
			}
			credential, err := a.vault.Get(c, asset.NativeWriteCredentialID)
			if err != nil || credential.ActiveVersion != writeCredentialVersion {
				return fault.Denied
			}
		}
		return service.RenewSession(c, sessionID, asset.Engine)
	}
	leaseDone := make(chan struct{})
	defer func() { cancel(); <-leaseDone }()
	go func() {
		defer close(leaseDone)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				checkCtx, done := context.WithTimeout(ctx, 3*time.Second)
				e := check(checkCtx)
				done()
				if e != nil {
					cancel()
					target.Close()
					return
				}
			}
		}
	}()
	policy, e := database.NewSQLPolicy()
	if e != nil {
		return
	}
	relay := mysqlproxy.Relay{Downstream: downstream.Conn.Conn, Upstream: target.Conn.Conn, Database: asset.Database, Check: check, Validate: func(query string) error {
		if nativeWrite {
			return policy.NativeDMLForDatabase(query, asset.Database)
		}
		return policy.ReadOnlyForDatabase(query, asset.Database)
	}, Audit: func(c context.Context, event mysqlproxy.QueryEvent) error {
		if event.Phase == "start" {
			var e error
			queryID, e = service.BeginQuery(c, sessionID, event.SQL, event.Command, event.Parameters)
			return e
		}
		if event.Unknown {
			return service.UncertainQuery(c, queryID, event.Duration)
		}
		return service.FinishQuery(c, queryID, event.Failed, event.Rows, event.Duration)
	}}
	_ = relay.Serve(ctx)
}

// MySQL sends the requested schema before authentication. Only validate it here;
// upstream connections and secret access are deferred until OnAuthSuccess.
type databaseHandshake struct {
	server.EmptyHandler
	requested string
}

func (h *databaseHandshake) UseDB(name string) error {
	// The identity/asset is not established until the full-auth callback. Record
	// the schema and let the ready phase validate it instead of accessing targets.
	if name == "" {
		return nil
	}
	h.requested = name
	return nil
}
