package database

import "testing"

func TestNativeDMLPolicy(t *testing.T) {
	p, err := NewSQLPolicy()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT id FROM items", "INSERT INTO inventory.items(id,label) VALUES (?,?)", "UPDATE items SET label=? WHERE id=?", "DELETE FROM items WHERE id=1", "BEGIN", "COMMIT", "ROLLBACK", "SET autocommit=0", "USE inventory"} {
		if err = p.NativeDMLForDatabase(q, "inventory"); err != nil {
			t.Errorf("allowed %s: %v", q, err)
		}
	}
	for _, q := range []string{"INSERT INTO other.items VALUES (1)", "UPDATE other.items SET label='bad'", "DELETE FROM other.items", "UPDATE items SET label=evil()", "INSERT INTO items SELECT evil()", "UPDATE items SET label=load_file('/etc/passwd')", "UPDATE items SET label=(SELECT label FROM other.items LIMIT 1)", "CREATE TABLE p(id int)", "DROP TABLE items", "CALL p()", "REPLACE INTO items VALUES (1)", "INSERT INTO items VALUES (1);DELETE FROM items", "/*!50000 DELETE FROM items */", "SET GLOBAL read_only=0", "SET SESSION TRANSACTION READ WRITE", "SELECT 1 INTO OUTFILE '/tmp/test'", "GRANT ALL ON inventory.* TO x"} {
		if err = p.NativeDMLForDatabase(q, "inventory"); err == nil {
			t.Errorf("denied query accepted: %s", q)
		}
	}
}
