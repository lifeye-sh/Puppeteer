package resourcebudget

import "testing"

func TestDatabaseBudgetsReserveMaintenanceConnections(t *testing.T) {
	for _, name := range []string{"light", "standard", "performance"} {
		b, e := Database(name)
		if e != nil || b.Connections < int(b.PoolMax)+10 || b.SharedMiB <= 0 || b.PoolMin >= b.PoolMax {
			t.Fatal(name, b, e)
		}
	}
	b, e := Database("")
	if e != nil || b.PoolMax != 10 || b.PoolMin != 1 {
		t.Fatal(b, e)
	}
	if _, e = Database("bad"); e == nil {
		t.Fatal("unknown budget accepted")
	}
}
