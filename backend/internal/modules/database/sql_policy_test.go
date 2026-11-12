package database

import (
	"strings"
	"testing"
)

func TestReadOnlyASTProfile(t *testing.T) {
	p, e := NewSQLPolicy()
	if e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{"select 1", "select count(*),sum(total) from orders", "with c as (select id from inventory) select * from c", "select id from a union all select id from b", "select version(),database(),@@session.autocommit", "select * from inventory where name=?", "select concat('delete',' from inventory')", "show engines", "show storage engines", "show plugins", "show charset", "show collation", "show tables", "show full columns from inventory", "show create table inventory", "show session variables like 'character_set%'"} {
		t.Run(sql, func(t *testing.T) {
			if e := p.ReadOnly(sql); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, sql := range []string{"", "select 1; delete from inventory", "insert into inventory values(1)", "with c as (select 1) delete from inventory", "select * from inventory for update", "select * from inventory into outfile '/tmp/leak'", "select load_file('/etc/passwd')", "select sleep(30)", "select benchmark(100000,md5('x'))", "select get_lock('lock',10)", "select custom_udf(1)", "select inventory.abs(1)", "select @x:=1", "call inventory.update_all()", "set global read_only=0", "/*!50000 DELETE FROM inventory */", "show create user root", "show grants", "select (select sleep(30))", strings.Repeat("x", MaxSQLBytes+1)} {
		t.Run(sql[:min(len(sql), 100)], func(t *testing.T) {
			if p.ReadOnly(sql) == nil {
				t.Fatal("unsafe SQL accepted")
			}
		})
	}
}

func TestClientDatabaseSelection(t *testing.T) {
	p, _ := NewSQLPolicy()
	if e := p.ReadOnlyForDatabase("USE `inventory`", "inventory"); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{"USE mysql", "USE inventory; DELETE FROM items", "/*! USE inventory */"} {
		if p.ReadOnlyForDatabase(query, "inventory") == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	if p.ReadOnly("USE inventory") == nil {
		t.Fatal("database selection requires a bound target")
	}
	if p.ReadOnly("START TRANSACTION READ WRITE") == nil {
		t.Fatal("readonly transaction override accepted")
	}
	if e := p.ReadOnly("START TRANSACTION READ ONLY"); e != nil {
		t.Fatal(e)
	}
}

func TestDesktopClientResultLimits(t *testing.T) {
	p, _ := NewSQLPolicy()
	for _, q := range []string{"use inventory", "SET sql_select_limit=200", "SET SESSION sql_select_limit=18446744073709551615", "SET sql_select_limit=DEFAULT", "SHOW TABLE STATUS FROM inventory LIKE 'items'"} {
		if e := p.ReadOnlyForDatabase(q, "inventory"); e != nil {
			t.Fatalf("%s: %v", q, e)
		}
	}
	for _, q := range []string{"SET GLOBAL sql_select_limit=200", "SET sql_select_limit=-1", "SET sql_select_limit=1+1", "SET sql_select_limit='200'", "SET sql_select_limit=18446744073709551616", "SET sql_select_limit=@user_limit", "SET sql_select_limit=200,transaction_read_only=0"} {
		if p.ReadOnlyForDatabase(q, "inventory") == nil {
			t.Fatalf("unsafe limit accepted: %s", q)
		}
	}
}
