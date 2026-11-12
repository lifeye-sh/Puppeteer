package database

import "testing"

func TestSessionSQLProfile(t *testing.T) {
	policy, e := NewSQLPolicy()
	if e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{"SET NAMES utf8mb4", "SET character_set_results=NULL", "SET autocommit=0", "SET SESSION autocommit=1", "SET SESSION transaction_isolation='READ-COMMITTED'", "BEGIN", "COMMIT", "ROLLBACK"} {
		if policy.ReadOnly(sql) != nil {
			t.Fatal(sql)
		}
	}
	for _, sql := range []string{"SET GLOBAL autocommit=1", "SET autocommit=sleep(20)", "SET NAMES gbk", "SET sql_mode='NO_BACKSLASH_ESCAPES'", "SET @x=1", "SET autocommit=1; DELETE FROM items"} {
		if policy.ReadOnly(sql) == nil {
			t.Fatal(sql)
		}
	}
}
