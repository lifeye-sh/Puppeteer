package mysqlproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"strings"
	"time"
)

var ErrTargetPrivileges = errors.New("target credential privileges exceed the selected database profile")

type Target struct {
	ProgramPermissions                                bool
	TLSMode                                           string
	Address, User, Password, Database, ServerName, CA string
	Timeout                                           time.Duration
}

func ConnectTarget(ctx context.Context, target Target, readonly bool) (*client.Conn, error) {
	roots := x509.NewCertPool()
	if target.TLSMode != "" && target.TLSMode != "verify-full" && target.TLSMode != "disable" {
		return nil, errors.New("invalid target TLS mode")
	}
	if target.TLSMode != "disable" && (target.ServerName == "" || !roots.AppendCertsFromPEM([]byte(target.CA))) {
		return nil, errors.New("target TLS trust is required")
	}
	if target.Timeout <= 0 || target.Timeout > time.Minute {
		return nil, errors.New("invalid target timeout")
	}
	connectCtx, cancel := context.WithTimeout(ctx, target.Timeout)
	defer cancel()
	c, e := client.ConnectWithContext(connectCtx, target.Address, target.User, target.Password, target.Database, target.Timeout, func(c *client.Conn) error {
		if target.TLSMode != "disable" {
			c.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.ServerName, RootCAs: roots})
		}
		c.UnsetCapability(mysql.CLIENT_LOCAL_FILES | mysql.CLIENT_MULTI_STATEMENTS | mysql.CLIENT_COMPRESS | mysql.CLIENT_DEPRECATE_EOF | mysql.CLIENT_QUERY_ATTRIBUTES)
		c.SetCapability(mysql.CLIENT_MULTI_RESULTS | mysql.CLIENT_PS_MULTI_RESULTS)
		c.ReadTimeout = 30 * time.Second
		c.WriteTimeout = 30 * time.Second
		return nil
	})
	if e != nil {
		return nil, e
	}
	if !target.ProgramPermissions {
		grants, e := c.Execute("SHOW GRANTS FOR CURRENT_USER()")
		if e != nil {
			c.Close()
			return nil, e
		}
		if grants.Resultset == nil || len(grants.Values) == 0 {
			c.Close()
			return nil, errors.New("target grants are missing")
		}
		for i := range grants.Values {
			text, e := grants.GetString(i, 0)
			if e != nil || !targetGrant(text, target.Database, readonly) {
				c.Close()
				return nil, ErrTargetPrivileges
			}
		}
	}
	if readonly {
		// Defense in depth for SECURITY DEFINER views/functions: SELECT-only
		// grants do not, by themselves, prevent their definer from writing.
		if _, e := c.Execute("SET SESSION TRANSACTION READ ONLY"); e != nil {
			c.Close()
			return nil, e
		}
	}
	return c, nil
}
func readonlyGrant(grant, database string) bool {
	return targetGrant(grant, database, true)
}
func targetGrant(grant, database string, readonly bool) bool {
	if !strings.HasPrefix(grant, "GRANT ") || strings.Contains(grant, "WITH GRANT OPTION") {
		return false
	}
	privilege, rest, ok := strings.Cut(strings.TrimPrefix(grant, "GRANT "), " ON ")
	if !ok {
		return false
	}
	target, _, ok := strings.Cut(rest, " TO ")
	if !ok {
		return false
	}
	if privilege == "USAGE" {
		return target == "*.*"
	}
	if target != "`"+strings.ReplaceAll(database, "`", "``")+"`.*" {
		return false
	}
	for _, name := range strings.Split(privilege, ",") {
		switch strings.TrimSpace(name) {
		case "SELECT", "SHOW VIEW":
		case "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP", "INDEX", "REFERENCES":
			if readonly {
				return false
			}
		default:
			return false
		}
	}
	return true
}
