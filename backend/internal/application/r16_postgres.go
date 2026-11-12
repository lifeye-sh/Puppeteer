package application

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/databaseauth"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/pgproxy"
)

func (a *App) postgresProtocol(tlsConfig *tls.Config) *pgproxy.Server {
	return &pgproxy.Server{TLS: tlsConfig, Lookup: func(ctx context.Context, wire, requested, remote string) (databaseauth.SCRAMMaterial, func(context.Context) (*pgproxy.Session, error), error) {
		var zero databaseauth.SCRAMMaterial
		username, alias, e := databaseauth.SplitIdentity(wire)
		if e != nil {
			return zero, nil, e
		}
		user, asset, version, e := a.databaseAccess(ctx, username, alias, 0, 0)
		if e != nil || asset.Engine != "postgresql" || requested != "" && requested != asset.Database {
			return zero, nil, fault.Denied
		}
		raw, e := a.vault.DatabaseVerifier(ctx, user.ID, version)
		if e != nil {
			return zero, nil, e
		}
		defer clear(raw)
		material, e := databaseauth.SCRAMCredentials(string(raw))
		if e != nil {
			return zero, nil, fault.Denied
		}
		return material, func(c context.Context) (*pgproxy.Session, error) {
			configuration, e := a.appliedConfiguration(c)
			if e != nil {
				return nil, e
			}
			service := database.New(a.db)
			current, targetAsset, _, e := a.databaseAccess(c, username, alias, version, asset.Version)
			if e != nil {
				return nil, e
			}
			sessionID, e := service.BeginSession(c, current.ID, targetAsset, version, remote)
			if e != nil {
				return nil, e
			}
			established := false
			defer func() {
				if !established {
					cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
					defer done()
					_ = service.EndSession(cleanup, sessionID, "postgresql_connection_failed")
				}
			}()
			var target pgproxy.Target
			var credentialVersion int64
			e = a.db.Within(c, func(tx context.Context) error {
				credential, secret, e := a.vault.DatabaseAuth(tx, targetAsset.ReadonlyCredentialID, current.ID, "postgresql_connection")
				if e != nil {
					return e
				}
				if credential.Kind != "password" {
					return fault.Denied
				}
				credentialVersion = credential.ActiveVersion
				target = pgproxy.Target{Address: net.JoinHostPort(targetAsset.Host, strconv.Itoa(targetAsset.Port)), User: credential.Username, Password: secret.Password, Database: targetAsset.Database, ServerName: targetAsset.TLSServerName, CA: targetAsset.TLSCA, Timeout: time.Duration(targetAsset.ConnectTimeout) * time.Second}
				return nil
			})
			if e != nil {
				return nil, e
			}
			target.QueryTimeoutSeconds = configuration.Values.Int("postgres-query-timeout-seconds")
			upstream, e := pgproxy.ConnectTarget(c, target)
			target.Password = ""
			if e != nil {
				return nil, e
			}
			established = true
			check := func(checkCtx context.Context) error {
				if a.maintenance.Load() {
					return fault.Denied
				}
				if _, _, _, e := a.databaseAccess(checkCtx, username, alias, version, asset.Version); e != nil {
					return e
				}
				credential, e := a.vault.Get(checkCtx, targetAsset.ReadonlyCredentialID)
				if e != nil {
					return e
				}
				if credential.ActiveVersion != credentialVersion {
					return fault.Denied
				}
				return service.RenewSession(checkCtx, sessionID, targetAsset.Engine)
			}
			return &pgproxy.Session{MaxRows: uint64(configuration.Values.Int("postgres-result-max-rows")), MaxBytes: configuration.Values.Int("postgres-result-max-mib") << 20, Target: upstream, Check: check, Start: func(ctx context.Context, sql string, command byte, parameters []pgproxy.Parameter) (string, error) {
				return service.BeginQuery(ctx, sessionID, sql, command, parameters)
			}, Finish: service.FinishQuery, Close: func() {
				cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				if e := service.EndSession(cleanup, sessionID, "postgresql_connection_closed"); e != nil {
					slog.Error("PostgreSQL audit finalization failed", "session", sessionID)
				}
			}}, nil
		}, nil
	}, Failed: func(ctx context.Context, remote string) {
		_ = a.audit.Operation(ctx, "", "database.login", "denied", "postgresql", remote)
	}}
}
func (a *App) RunPostgresProxy(ctx context.Context) {
	if a.config.PostgresProxyAddr == "" {
		return
	}
	for ctx.Err() == nil {
		e := a.servePostgresProxy(ctx)
		a.postgresProxyReady.Store(false)
		if ctx.Err() != nil {
			return
		}
		slog.Error("PostgreSQL proxy stopped", "error", e)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (a *App) servePostgresProxy(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tlsConfig, e := a.databaseProxyTLS(ctx)
	if e != nil {
		return e
	}
	protocol := a.postgresProtocol(tlsConfig)
	listener, e := net.Listen("tcp", a.config.PostgresProxyAddr)
	if e != nil {
		return e
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	a.postgresProxyReady.Store(true)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	slots := make(chan struct{}, 64)
	for {
		raw, e := listener.Accept()
		if e != nil {
			return e
		}
		host, _, _ := net.SplitHostPort(raw.RemoteAddr().String())
		if !a.allowAttempt("postgres-connect:" + host) {
			raw.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			if !a.enterRequest() {
				raw.Close()
				return
			}
			defer a.requests.RUnlock()
			protocol.ServeConn(ctx, raw)
		}()
	}
}
