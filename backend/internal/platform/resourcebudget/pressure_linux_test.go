package resourcebudget

import "testing"

func TestCgroupPressure(t *testing.T) {
	for _, tc := range []struct{ used, limit, want string }{
		{"950", "1000", "export_memory_pressure"}, {"1001", "1000", "export_memory_pressure"}, {"900", "1000", ""},
		{"1000", "max", ""}, {"1000", "9223372036854771712", ""}, {"bad", "1000", "resource_probe_unavailable"}, {"1", "0", "resource_probe_unavailable"},
	} {
		err := cgroupPressure(tc.used, tc.limit)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
