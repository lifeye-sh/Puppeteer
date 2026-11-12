package automation

import (
	"testing"
	"time"
)

func TestCronTimezonesAndDST(t *testing.T) {
	start := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	next, e := NextRuns("30 2 * * *", "America/New_York", start)
	if e != nil {
		t.Fatal(e)
	}
	// 02:30 does not exist on March 8. The next occurrence must be March 9.
	if next[0].UTC().Format(time.RFC3339) != "2026-03-09T06:30:00Z" {
		t.Fatal(next[0])
	}
	fall, e := NextRuns("30 1 * * *", "America/New_York", time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	if fall[0].UTC().Hour() != 5 || fall[1].UTC().Hour() != 6 || fall[0].Day() != fall[1].Day() {
		t.Fatal("repeated local time must have distinct UTC instants", fall[:2])
	}
	c, _ := ParseSchedule("*/5 * * * *", "Asia/Shanghai")
	preview, _ := NextRuns("*/5 * * * *", "Asia/Shanghai", start)
	if !preview[0].Equal(c.Next(start)) || len(preview) != 10 {
		t.Fatal(preview)
	}
	for _, spec := range []string{"@every 1s", "* * * * * *", "0 0 31 2 *", "TZ=UTC * * * * *"} {
		if _, e = NextRuns(spec, "UTC", start); e == nil {
			t.Fatal("accepted invalid schedule", spec)
		}
	}
	if _, e = NextRuns("* * * * *", "No/SuchZone", start); e == nil {
		t.Fatal("invalid zone")
	}
}
