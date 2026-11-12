package mysqlproxy

import (
	"context"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"strings"
)

var ErrDMLPrivileges = errors.New("native DML credential must have database-scoped SELECT/INSERT/UPDATE/DELETE only")

func dmlGrant(grant, database string) bool {
	if !targetGrant(grant, database, false) {
		return false
	}
	privilege, _, ok := strings.Cut(strings.TrimPrefix(grant, "GRANT "), " ON ")
	if !ok {
		return false
	}
	for _, p := range strings.Split(privilege, ",") {
		switch strings.TrimSpace(p) {
		case "USAGE", "SELECT", "INSERT", "UPDATE", "DELETE":
		default:
			return false
		}
	}
	return true
}

func ConnectDMLTarget(ctx context.Context, target Target) (*client.Conn, error) {
	c, err := ConnectTarget(ctx, target, false)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	grants, err := c.Execute("SHOW GRANTS FOR CURRENT_USER()")
	if err == nil && grants != nil && grants.Resultset != nil && len(grants.Values) > 0 {
		for i := range grants.Values {
			var text string
			text, err = grants.GetString(i, 0)
			if err != nil || !dmlGrant(text, target.Database) {
				err = ErrDMLPrivileges
				break
			}
		}
	} else {
		err = ErrDMLPrivileges
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
