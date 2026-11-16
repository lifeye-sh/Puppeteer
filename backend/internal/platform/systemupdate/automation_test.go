package systemupdate

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAutomationWindowMidnightExpiryAndDST(t *testing.T) {
	now := time.Date(2026, 11, 16, 16, 30, 0, 0, time.UTC) // Tuesday 00:30 in Shanghai
	p := DefaultAutomation()
	p.Mode = "window"
	p.MaxVersion = "beta0.11"
	p.Start = "23:30"
	p.Weekdays = []int{1}
	p.ExpiresAt = now.Add(time.Hour)
	if e := p.Validate(now); e != nil {
		t.Fatal(e)
	}
	slot, end, ok := p.Window(now)
	if !ok || slot != "2026-11-16/23:30" || !end.Equal(now.Add(time.Hour)) {
		t.Fatal(slot, end, ok)
	}
	if _, _, ok = p.Window(end); ok {
		t.Fatal("window end accepted")
	}
	p.Timezone = "America/New_York"
	p.Start = "01:00"
	p.Weekdays = []int{0}
	p.ExpiresAt = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	first := time.Date(2026, 11, 1, 5, 15, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	a, _, aok := p.Window(first)
	b, _, bok := p.Window(second)
	if !aok || !bok || a != b {
		t.Fatal("DST fold duplicated or lost slot", a, b, aok, bok)
	}
	p.Start = "02:30"
	p.ExpiresAt = time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	if _, _, ok = p.Window(time.Date(2026, 3, 8, 7, 45, 0, 0, time.UTC)); ok {
		t.Fatal("nonexistent spring window accepted")
	}
}

func TestAutomationValidationAndVersionCeiling(t *testing.T) {
	now := time.Now()
	p := DefaultAutomation()
	p.Mode = "prepare"
	p.MaxVersion = "beta0.12"
	p.ExpiresAt = now.Add(time.Hour)
	if e := p.Validate(now); e != nil {
		t.Fatal(e)
	}
	c := Catalog{Releases: []Release{{Version: "beta0.11", Channel: "beta", MinSource: "beta0.10"}, {Version: "beta0.12", Channel: "beta", MinSource: "beta0.10"}, {Version: "beta0.13", Channel: "beta", MinSource: "beta0.10"}}}
	r, ok := p.Select(c, "beta0.10")
	if !ok || r.Version != "beta0.12" {
		t.Fatal(r, ok)
	}
	p.Channel = "stable"
	if _, ok = p.Select(c, "beta0.10"); ok {
		t.Fatal("cross channel")
	}
	p.ExpiresAt = now.Add(31 * 24 * time.Hour)
	if p.Validate(now) == nil {
		t.Fatal("unbounded authorization")
	}
	p.ExpiresAt = now.Add(time.Hour)
	p.Weekdays = []int{1, 1}
	if p.Validate(now) == nil {
		t.Fatal("duplicate day")
	}
}

func TestPreparedTransactionNeverStopsBusiness(t *testing.T) {
	tx, _ := transactionFixture(t)
	tx.journal.Plan.PrepareOnly = true
	tx.journal.PlanDigest = planDigest(tx.journal.Plan)
	d := &upgradeFixture{}
	if e := tx.Run(context.Background(), d); e != nil {
		t.Fatal(e)
	}
	if tx.View().Stage != "prepared" || !reflect.DeepEqual(d.calls, []string{"prepare"}) || UpgradeBlocked(filepath.Dir(tx.path)) {
		t.Fatal(tx.View().Stage, d.calls)
	}
}

func TestExpiredWindowAfterDownloadNeverQuiesces(t *testing.T) {
	tx, _ := transactionFixture(t)
	deadline := time.Now().Add(-time.Second)
	tx.journal.Plan.StartBefore = &deadline
	tx.journal.PlanDigest = planDigest(tx.journal.Plan)
	d := &upgradeFixture{}
	if e := tx.Run(context.Background(), d); e == nil {
		t.Fatal("expired window executed")
	}
	if tx.View().Stage != "failed" || !reflect.DeepEqual(d.calls, []string{"prepare"}) {
		t.Fatal(tx.View().Stage, d.calls)
	}
}
