package database

import "testing"

func TestFrozenWorkOrderSQLProfile(t *testing.T) {
	p, e := NewSQLPolicy()
	if e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{"insert into items(id) values(3); update items set label='x' where id=3; select * from items", "create table example(id int); alter table example add name varchar(10); drop table example"} {
		if _, e = p.WorkOrder(sql, "inventory"); e != nil {
			t.Fatal(sql, e)
		}
	}
	for _, sql := range []string{"create user hacker identified by 'x'", "delete from other.items", "update items set label=load_file('/etc/passwd')", "call inventory.run()", "create table x as select load_file('/etc/passwd')", "select get_lock('x',30)", "insert into items(id) values(3); create table x(id int)", "set autocommit=0; delete from items", "select 1 into outfile '/tmp/exfil'"} {
		if _, e = p.WorkOrder(sql, "inventory"); e == nil {
			t.Fatal(sql)
		}
	}
}

func TestWorkOrderPhysicalTablesExcludeColumnQualifiers(t *testing.T) {
	p, _ := NewSQLPolicy()
	for _, sql := range []string{"UPDATE items SET label='x' WHERE id=1", "UPDATE items t SET t.label='x' WHERE t.id=1", "DELETE FROM items WHERE id=1", "INSERT INTO items(id,label) VALUES(1,'x')"} {
		plan, e := p.WorkOrder(sql, "inventory")
		if e != nil || len(plan.WriteTables) != 1 || plan.WriteTables[0] != "items" {
			t.Fatalf("physical tables for %s: %v %v", sql, plan.WriteTables, e)
		}
	}
}
