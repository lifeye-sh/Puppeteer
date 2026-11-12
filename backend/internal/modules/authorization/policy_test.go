package authorization

import (
	"testing"
	"time"
)

func TestAddressFamiliesAndBoundaries(t *testing.T) {
	for _, v := range []struct {
		ip      string
		entries []string
		want    bool
	}{
		{"10.0.0.255", []string{"10.0.0.0/24"}, true}, {"10.0.1.0", []string{"10.0.0.0/24"}, false},
		{"2001:db8::ffff", []string{"2001:db8::/64"}, true}, {"2001:db9::1", []string{"2001:db8::/64"}, false},
		{"192.0.2.10", []string{"192.0.2.10-192.0.2.20"}, true}, {"192.0.2.21", []string{"192.0.2.10-192.0.2.20"}, false},
		{"::ffff:192.0.2.10", []string{"192.0.2.0/24"}, true}, {"untrusted", []string{"0.0.0.0/0"}, false},
	} {
		if got := IPMatches(v.ip, v.entries); got != v.want {
			t.Errorf("%s in %v = %v", v.ip, v.entries, got)
		}
	}
}
func TestPeriodOvernightUsesStartingWeekday(t *testing.T) {
	p := []Period{{Days: []int{1}, Start: "22:00", End: "06:00"}}
	for _, v := range []struct {
		when string
		want bool
	}{{"2026-10-26T23:00:00Z", true}, {"2026-10-27T05:59:00Z", true}, {"2026-10-27T06:00:00Z", false}, {"2026-10-25T23:00:00Z", false}} {
		now, _ := time.Parse(time.RFC3339, v.when)
		if matchesTime(p, now) != v.want {
			t.Error(v.when)
		}
	}
}
