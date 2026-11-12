package mysqlproxy

import "testing"

func TestDMLTargetGrants(t *testing.T) {
	for _, p := range []string{"USAGE ON *.*", "SELECT, INSERT, UPDATE, DELETE ON `inventory`.*", "INSERT ON `inventory`.*"} {
		if !dmlGrant("GRANT "+p+" TO `limited`@`%`", "inventory") {
			t.Fatal(p)
		}
	}
	for _, p := range []string{"ALL PRIVILEGES ON `inventory`.*", "SELECT, INSERT, CREATE ON `inventory`.*", "SELECT, SHOW VIEW ON `inventory`.*", "SELECT, EXECUTE ON `inventory`.*", "SELECT, TRIGGER ON `inventory`.*", "SELECT, INSERT ON *.*", "SELECT, INSERT ON `other`.*", "`role`@`%`", "SELECT ON `inventory`.* TO `limited`@`%` WITH GRANT OPTION"} {
		if dmlGrant("GRANT "+p+" TO `limited`@`%`", "inventory") {
			t.Fatal(p)
		}
	}
}
