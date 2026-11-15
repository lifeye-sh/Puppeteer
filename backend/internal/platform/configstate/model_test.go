package configstate

import (
	"testing"
	"time"
)

func TestEvidenceMustMatchRevisionNodeGenerationAndLifetime(t *testing.T) {
	now := time.Now()
	gen := uint64(4)
	base := Task{Node: "a", Revision: 3, Generation: &gen, State: "succeeded", Expires: now.Add(time.Minute), Deadline: now.Add(time.Second)}
	if got := Dependency(&base, "a", 3, &gen, now); got != "ready" {
		t.Fatal(got)
	}
	other := uint64(5)
	for _, tc := range []struct {
		node     string
		revision int64
		gen      *uint64
		now      time.Time
	}{{"b", 3, &gen, now}, {"a", 4, &gen, now}, {"a", 3, &other, now}, {"a", 3, nil, now}, {"a", 3, &gen, base.Expires}} {
		if got := Dependency(&base, tc.node, tc.revision, tc.gen, tc.now); got != "stale" {
			t.Fatal(tc, got)
		}
	}
	base.State = "running"
	if Dependency(&base, "a", 3, &gen, now) != "checking" {
		t.Fatal("running")
	}
	if Dependency(&base, "a", 3, &gen, base.Deadline) != "stale" {
		t.Fatal("deadline")
	}
	if Normalize(base, base.Deadline).State != "interrupted" {
		t.Fatal("restart/timeout must not remain running")
	}
	base.State = "failed"
	if Dependency(&base, "a", 3, &gen, now) != "blocked" {
		t.Fatal("failure")
	}
	base.State = "cancelled"
	if Dependency(&base, "a", 3, &gen, now) != "stale" {
		t.Fatal("cancelled")
	}
	if Dependency(nil, "a", 3, &gen, now) != "unknown" {
		t.Fatal("missing evidence")
	}
}

func TestRegistryDoesNotDeployPolicyOrExternalDependencies(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Features {
		if seen[f.ID] {
			t.Fatal("duplicate", f.ID)
		}
		seen[f.ID] = true
		if f.Protocol != "" && f.Category != "deployment" {
			t.Fatal("non-deployment adapter", f.ID)
		}
	}
	for _, id := range []string{"recording", "ai", "forward", "databasePolicy", "mail"} {
		f, ok := Find(id)
		if !ok || f.Protocol != "" {
			t.Fatal(id)
		}
	}
	f, _ := Find("databasePolicy")
	if !f.Invert {
		t.Fatal("block DML is inverse of desired write access")
	}
}
