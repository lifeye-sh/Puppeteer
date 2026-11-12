package backup

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PGTool passes connection secrets through a private passfile, never argv.
// Callers supply an isolated target for restore; this helper never creates or
// drops databases and deliberately rejects pg_restore's destructive switches.
func PGTool(ctx context.Context, tool, dsn, privateDir string, args []string, out io.Writer) error {
	if tool != "pg_dump" && tool != "pg_restore" {
		return errors.New("unsupported PostgreSQL tool")
	}
	version, e := exec.CommandContext(ctx, tool, "--version").Output()
	if e != nil || !regexp.MustCompile(`^`+tool+` \(PostgreSQL\) 18(?:\.|\s|$)`).Match(version) {
		return errors.New("PostgreSQL 18 client tools required")
	}
	for _, arg := range args {
		if arg == "--clean" || arg == "--create" || arg == "-c" || arg == "-C" || strings.HasPrefix(arg, "--dbname") || strings.HasPrefix(arg, "--host") || strings.HasPrefix(arg, "--username") {
			return errors.New("connection or destructive option prohibited")
		}
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		return errors.New("invalid database connection configuration")
	}
	transport, e := pgTransportEnvironment(dsn)
	if e != nil {
		return e
	}
	if tool == "pg_restore" {
		if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`).MatchString(cfg.Database) {
			return errors.New("unsupported restore database name")
		}
		args = append([]string{"--dbname=" + cfg.Database}, args...)
	}
	escape := func(v string) (string, error) {
		if strings.ContainsAny(v, "\r\n") {
			return "", errors.New("invalid passfile field")
		}
		return strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(v), nil
	}
	parts := []string{cfg.Host, strconv.Itoa(int(cfg.Port)), cfg.Database, cfg.User, cfg.Password}
	for i := range parts {
		parts[i], e = escape(parts[i])
		if e != nil {
			return e
		}
	}
	pass, e := os.CreateTemp(privateDir, ".pgpass-")
	if e != nil {
		return e
	}
	defer os.Remove(pass.Name())
	if e = pass.Chmod(0600); e == nil {
		_, e = io.WriteString(pass, strings.Join(parts, ":")+"\n")
	}
	closeErr := pass.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "PG") {
			env = append(env, v)
		}
	}
	cmd.Env = append(env, "PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)), "PGDATABASE="+cfg.Database, "PGUSER="+cfg.User, "PGPASSFILE="+pass.Name(), "PGCONNECT_TIMEOUT=10")
	cmd.Env = append(cmd.Env, transport...)
	cmd.Stdout = out
	// Tool diagnostics can include SQL data or credentials. Never return them
	// through user-facing errors; detailed restore verification is separate.
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return errors.New(tool + " failed")
	}
	return nil
}

// libpq must use the same explicit TLS mode and trust files as the Go client.
// Strip inherited PG variables above so host settings cannot downgrade policy.
func pgTransportEnvironment(dsn string) ([]string, error) {
	u, e := url.Parse(dsn)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("PostgreSQL tools require a connection URI")
	}
	q := u.Query()
	if os.Getenv("PGSERVICE") != "" {
		return nil, errors.New("PostgreSQL service environment is not supported by backup tools")
	}
	for _, values := range q {
		if len(values) > 1 {
			return nil, errors.New("duplicate PostgreSQL URI parameter")
		}
	}
	mode := q.Get("sslmode")
	if mode == "" {
		mode = os.Getenv("PGSSLMODE")
	}
	if mode == "" {
		mode = "prefer"
	}
	switch mode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return nil, errors.New("unsupported PostgreSQL TLS mode")
	}
	if q.Get("sslpassword") != "" || q.Get("service") != "" {
		return nil, errors.New("unsupported PostgreSQL TLS service settings")
	}
	env := []string{"PGSSLMODE=" + mode}
	for _, v := range []struct{ param, name string }{{"sslrootcert", "PGSSLROOTCERT"}, {"sslcert", "PGSSLCERT"}, {"sslkey", "PGSSLKEY"}} {
		value := q.Get(v.param)
		if value == "" {
			value = os.Getenv(v.name)
		}
		if value != "" {
			if strings.ContainsAny(value, "\x00\r\n") {
				return nil, errors.New("invalid TLS file path")
			}
			env = append(env, v.name+"="+value)
		}
	}
	return env, nil
}

// DumpSnapshot holds a REPEATABLE READ transaction until pg_dump completes.
// CA and encrypted business secrets share this database snapshot. The caller
// supplies the exported keyring from its already-drained maintenance window.
func DumpSnapshot(ctx context.Context, dsn, privateDir string) (path string, err error) {
	return dumpSnapshot(ctx, dsn, privateDir, nil, false)
}
func dumpSnapshot(ctx context.Context, dsn, privateDir string, inspect func(pgx.Tx) error, excludePayload bool) (path string, err error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", errors.New("snapshot database unavailable")
	}
	defer conn.Close(context.Background())
	if err = PrepareReader(ctx, conn); err != nil {
		return "", err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{ReaderRole}.Sanitize()); err != nil {
		return "", err
	}
	var version int
	var snapshot string
	if err = tx.QueryRow(ctx, "SELECT current_setting('server_version_num')::int,pg_export_snapshot()").Scan(&version, &snapshot); err != nil {
		return
	}
	if version/10000 != 18 {
		return "", errors.New("PostgreSQL 18 required")
	}
	if inspect != nil {
		if err = inspect(tx); err != nil {
			return "", err
		}
	}
	// A deterministic manifest identifies the data boundary; payload exclusions
	// are selected by the higher-level archive workflow, not hidden here.
	path = filepath.Join(privateDir, "database.dump")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	args := []string{"--format=custom", "--no-owner", "--no-acl", "--schema=public", "--role=" + ReaderRole, "--snapshot=" + snapshot}
	if excludePayload {
		for _, table := range payloadTables {
			args = append(args, "--exclude-table-data=public."+table.name)
		}
	}
	err = PGTool(ctx, "pg_dump", dsn, privateDir, args, f)
	if err == nil {
		err = f.Sync()
	}
	e = f.Close()
	if err == nil {
		err = e
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	err = tx.Commit(ctx)
	return
}
