package database

import (
	"strings"
	"vitess.io/vitess/go/vt/sqlparser"
)

type WorkPlan struct {
	Mode           string
	Statements     []string
	WriteTables    []string
	ReadStatements []bool
}

func (p *SQLPolicy) WorkOrder(query, database string) (plan WorkPlan, e error) {
	if len(query) == 0 || len(query) > MaxSQLBytes || strings.ContainsAny(query, "\x00") || strings.Contains(query, "/*!") {
		return plan, ErrSQLDenied
	}
	pieces, e := p.parser.SplitStatementToPieces(query)
	if e != nil || len(pieces) == 0 || len(pieces) > 100 {
		return plan, ErrSQLDenied
	}
	for _, piece := range pieces {
		statement, e := p.parser.Parse(piece)
		if e != nil {
			return plan, ErrSQLDenied
		}
		mode := "transaction"
		writes := false
		reads := false
		switch statement.(type) {
		case *sqlparser.Insert, *sqlparser.Update, *sqlparser.Delete:
			writes = true
		case *sqlparser.CreateTable, *sqlparser.AlterTable, *sqlparser.DropTable, *sqlparser.TruncateTable, *sqlparser.RenameTable:
			mode = "ddl"
		case *sqlparser.Select, *sqlparser.Union:
			reads = true
			if p.ReadOnly(piece) != nil {
				return plan, ErrSQLDenied
			}
		default:
			return plan, ErrSQLDenied
		}
		if plan.Mode != "" && plan.Mode != mode {
			return plan, ErrSQLDenied
		}
		plan.Mode = mode
		e = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
			switch v := node.(type) {
			case *sqlparser.AliasedTableExpr:
				// Column qualifiers also contain TableName nodes; they are not
				// physical tables (and may be empty or an alias such as `t`).
				if table, ok := v.Expr.(sqlparser.TableName); writes && ok && !table.Name.IsEmpty() {
					plan.WriteTables = append(plan.WriteTables, table.Name.String())
				}
			case sqlparser.TableName:
				if !v.Qualifier.IsEmpty() && v.Qualifier.String() != database {
					return false, ErrSQLDenied
				}
			case *sqlparser.FuncExpr:
				if !v.Qualifier.IsEmpty() {
					return false, ErrSQLDenied
				}
				if _, ok := readFunctions[strings.ToLower(v.Name.String())]; !ok {
					return false, ErrSQLDenied
				}
			case *sqlparser.Select:
				if v.Into != nil {
					return false, ErrSQLDenied
				}
			case *sqlparser.AssignmentExpr, *sqlparser.LockingFunc:
				return false, ErrSQLDenied
			}
			return true, nil
		}, statement)
		if e != nil {
			return plan, e
		}
		plan.Statements = append(plan.Statements, piece)
		plan.ReadStatements = append(plan.ReadStatements, reads)
	}
	return plan, nil
}
