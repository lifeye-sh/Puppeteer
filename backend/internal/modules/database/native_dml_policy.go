package database

import "vitess.io/vitess/go/vt/sqlparser"

// NativeDMLForDatabase keeps all read-profile restrictions and additionally
// permits one parsed INSERT/UPDATE/DELETE against the selected database.
// DDL, CALL, file operations and unknown functions still require other workflows.
// Target credentials independently enforce database scope and DML-only grants.
func (p *SQLPolicy) NativeDMLForDatabase(query, database string) error {
	if p.ReadOnlyForDatabase(query, database) == nil {
		return nil
	}
	plan, err := p.WorkOrder(query, database)
	if err != nil || plan.Mode != "transaction" || len(plan.Statements) != 1 || len(plan.ReadStatements) != 1 || plan.ReadStatements[0] {
		return ErrSQLDenied
	}
	stmt, err := p.parser.Parse(query)
	if err != nil {
		return ErrSQLDenied
	}
	switch s := stmt.(type) {
	case *sqlparser.Insert:
		if s.Action != sqlparser.InsertAct {
			return ErrSQLDenied
		}
	case *sqlparser.Update, *sqlparser.Delete:
	default:
		return ErrSQLDenied
	}
	return sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		if s, ok := node.(*sqlparser.Select); ok && (s.Into != nil || s.Lock != sqlparser.NoLock) {
			return false, ErrSQLDenied
		}
		return true, nil
	}, stmt)
}
