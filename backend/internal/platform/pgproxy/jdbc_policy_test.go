package pgproxy

import "testing"

func TestJDBCReadonlyOptions(t *testing.T) {
	for _, sql := range []string{`SELECT "count|sum"(1)`, `SELECT public._pg_expandarray(ARRAY[1])`, `SELECT information_schema.set_config('search_path','public',false)`} {
		if ValidateSQL(sql) == nil {
			t.Errorf("unapproved qualified/function name accepted: %s", sql)
		}
	}
	for _, sql := range []string{"BEGIN READ ONLY", "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", "SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY", "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED", "SET TRANSACTION READ ONLY", "SELECT 'a'||'b'", "SELECT 1 BETWEEN 0 AND 2"} {
		if e := ValidateSQL(sql); e != nil {
			t.Errorf("readonly rejected: %s", sql)
		}
	}
	for _, sql := range []string{"BEGIN READ WRITE", "BEGIN READ ONLY READ WRITE", "SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE", "SET TRANSACTION READ WRITE", "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL SERIALIZABLE, READ WRITE", "SELECT 'a' OPERATOR(public.||) 'b'", "SELECT pg_catalog.set_config('search_path','public',false)", "SET search_path=public"} {
		if e := ValidateSQL(sql); e == nil {
			t.Errorf("unsafe accepted: %s", sql)
		}
	}
}
