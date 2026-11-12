// Package pgproxy provides the PostgreSQL-specific target connection boundary.
package pgproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrPrivileges = errors.New("postgresql readonly role privileges exceed policy")

type Target struct {
	Address, User, Password, Database, ServerName, CA string
	Timeout                                           time.Duration
	QueryTimeoutSeconds                               int
}

func ConnectTarget(ctx context.Context, t Target) (*pgx.Conn, error) {
	cfg, e := targetConfig(t)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	c, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			_ = c.Close(context.Background())
		}
	}()
	if _, e = c.Exec(ctx, "SET default_transaction_read_only = on"); e != nil {
		return nil, e
	}
	if e = validateReadonly(ctx, c); e != nil {
		return nil, e
	}
	var one int
	if e = c.QueryRow(ctx, "SELECT 1").Scan(&one); e != nil {
		return nil, e
	}
	if one != 1 {
		return nil, errors.New("unexpected PostgreSQL probe response")
	}
	success = true
	return c, nil
}
func targetConfig(t Target) (*pgx.ConnConfig, error) {
	host, port, e := net.SplitHostPort(t.Address)
	p, pe := strconv.Atoi(port)
	if e != nil || pe != nil || p < 1 || p > 65535 || host == "" || strings.ContainsAny(host, "/\\\x00\r\n") || t.User == "" || t.Database == "" || t.Timeout <= 0 || t.Timeout > time.Minute {
		return nil, errors.New("invalid PostgreSQL target")
	}
	roots := x509.NewCertPool()
	if t.ServerName == "" || !roots.AppendCertsFromPEM([]byte(t.CA)) {
		return nil, errors.New("PostgreSQL target TLS trust required")
	}
	cfg, e := pgx.ParseConfig("postgresql://localhost/postgres?sslmode=verify-full&require_auth=scram-sha-256")
	if e != nil {
		return nil, e
	}
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = host, uint16(p), t.User, t.Password, t.Database
	cfg.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName, RootCAs: roots}
	cfg.Fallbacks = nil
	cfg.ValidateConnect = nil
	cfg.AfterConnect = nil
	cfg.ConnectTimeout = t.Timeout
	cfg.DialFunc = (&net.Dialer{Timeout: t.Timeout}).DialContext
	cfg.RuntimeParams = map[string]string{"application_name": "next-terminal-readonly-proxy", "statement_timeout": "10000", "lock_timeout": "3000", "search_path": "pg_catalog, public"}
	if t.QueryTimeoutSeconds != 0 {
		if t.QueryTimeoutSeconds < 1 || t.QueryTimeoutSeconds > 20 {
			return nil, errors.New("invalid query timeout")
		}
		cfg.RuntimeParams["statement_timeout"] = strconv.Itoa(t.QueryTimeoutSeconds * 1000)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return cfg, nil
}

// Inspect effective privileges including PUBLIC grants, column ACLs, role
// membership and ownership. A transaction-readonly setting alone is insufficient.
// Reject application functions (including SECURITY DEFINER) and foreign tables
// rather than assuming their bodies or remote effects are read-only.
func validateReadonly(ctx context.Context, c *pgx.Conn) error {
	var unsafe bool
	e := c.QueryRow(ctx, `
 SELECT
 EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND
   (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls))
 OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname<>current_user AND pg_has_role(current_user,oid,'MEMBER'))
 OR EXISTS(SELECT 1 FROM pg_database WHERE datname=current_database() AND
   (datdba=(SELECT oid FROM pg_roles WHERE rolname=current_user)
    OR has_database_privilege(current_user,oid,'CREATE,TEMPORARY')))
 OR EXISTS(SELECT 1 FROM pg_namespace WHERE nspname !~ '^pg_temp_' AND nspname !~ '^pg_toast_temp_' AND
   (nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) OR has_schema_privilege(current_user,oid,'CREATE')))
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname !~ '^pg_toast' AND
   ((c.relkind IN ('r','p','v','m','f') AND
     (c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
      OR has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
      OR has_any_column_privilege(current_user,c.oid,'INSERT,UPDATE,REFERENCES')
      OR (c.relkind='f' AND has_table_privilege(current_user,c.oid,'SELECT'))))
    OR (c.relkind='S' AND has_sequence_privilege(current_user,c.oid,'USAGE,UPDATE'))))
 OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND has_function_privilege(current_user,p.oid,'EXECUTE'))
 `).Scan(&unsafe)
	if e != nil {
		return e
	}
	if unsafe {
		return ErrPrivileges
	}
	return nil
}
