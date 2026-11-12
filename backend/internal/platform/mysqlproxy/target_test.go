package mysqlproxy

import "testing"

func TestTargetPrivilegeBoundary(t *testing.T) {
	for _, grant := range []string{"GRANT USAGE ON *.* TO `reader`@`%` REQUIRE SSL", "GRANT SELECT, SHOW VIEW ON `inventory`.* TO `reader`@`%`"} {
		if !readonlyGrant(grant, "inventory") {
			t.Fatal(grant)
		}
	}
	for _, grant := range []string{"GRANT SELECT ON *.* TO `reader`@`%`", "GRANT SELECT ON `other`.* TO `reader`@`%`", "GRANT SELECT, INSERT ON `inventory`.* TO `reader`@`%`", "GRANT ALL PRIVILEGES ON `inventory`.* TO `reader`@`%`", "GRANT SELECT ON `inventory`.* TO `reader`@`%` WITH GRANT OPTION", "GRANT `writer`@`%` TO `reader`@`%`", ""} {
		if readonlyGrant(grant, "inventory") {
			t.Fatal(grant)
		}
	}
}
