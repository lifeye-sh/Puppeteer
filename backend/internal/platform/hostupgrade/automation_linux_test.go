//go:build linux

package hostupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/systemupdate"
)

func TestAutomationFailureSuspendsEvenAfterNewerManualTask(t *testing.T) {
	root := t.TempDir()
	c := &Controller{Executor: deploytask.ComposeExecutor{Directory: root}}
	j := progressFixture(t, c.directory(), "rolled_back", time.Now())
	raw, _ := json.Marshal(j)
	if e := os.WriteFile(filepath.Join(c.directory(), j.Plan.ID+".json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	// The current slot can have moved on; the failed automatic result remains in
	// its immutable archive and must still suspend unattended retries.
	if e := os.Remove(filepath.Join(c.directory(), "upgrade.json")); e != nil {
		t.Fatal(e)
	}
	p := systemupdate.DefaultAutomation()
	p.Mode = "window"
	p.MaxVersion = "beta0.11"
	p.ExpiresAt = time.Now().Add(time.Hour)
	if e := c.saveAutomation(AutomationState{Policy: p, Actor: "admin", LastTask: j.Plan.ID}); e != nil {
		t.Fatal(e)
	}
	c.automationTick(context.Background(), time.Now())
	state, e := c.automationState()
	if e != nil || !state.Suspended || state.LastStage != "rolled_back" {
		t.Fatal(state, e)
	}
	if len(state.Notices) != 1 || state.Notices[0].Task != j.Plan.ID {
		t.Fatal("missing durable terminal notice", state)
	}
	if c.reconcileAutomation(&state) || len(state.Notices) != 1 {
		t.Fatal("duplicate terminal notice", state)
	}
}

func TestRetentionKeepsRecoveryAndRecentSuccessAndNeverFollowsLink(t *testing.T) {
	root := t.TempDir()
	c := &Controller{Executor: deploytask.ComposeExecutor{Directory: root}}
	now := time.Now()
	if e := os.MkdirAll(c.directory(), 0700); e != nil {
		t.Fatal(e)
	}
	for i, stage := range []string{"succeeded", "succeeded", "succeeded", "succeeded", "rolled_back", "recovery_required", "prepared", "prepared"} {
		id := fmt.Sprintf("%032x", i+1)
		p := systemupdate.UpgradePlan{ID: id, Source: "beta0.10", Target: "beta0.11", LayoutDigest: strings.Repeat("b", 64)}
		raw, _ := json.Marshal(p)
		sum := sha256.Sum256(raw)
		j := systemupdate.UpgradeJournal{Format: 1, Plan: p, PlanDigest: hex.EncodeToString(sum[:]), Stage: stage, Updated: now.Add(-time.Duration(40+i) * 24 * time.Hour)}
		raw, _ = json.Marshal(j)
		if e := os.WriteFile(filepath.Join(c.directory(), id+".json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		if i == 7 {
			outside := t.TempDir()
			if e := os.WriteFile(filepath.Join(outside, "preserve"), []byte("protected"), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, filepath.Join(c.directory(), id)); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if _, e := os.Stat(filepath.Join(outside, "preserve")); e != nil {
					t.Error("followed cleanup symlink")
				}
			})
		} else {
			_ = os.Mkdir(filepath.Join(c.directory(), id), 0700)
		}
	}
	if e := c.pruneArtifacts(now, 30); e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("%032x", i)
		_, e := os.Lstat(filepath.Join(c.directory(), id))
		removed := os.IsNotExist(e)
		if removed != (i == 4 || i == 7) {
			t.Fatal("unsafe retention decision", i, removed)
		}
		if _, e = os.Stat(filepath.Join(c.directory(), id+".json")); e != nil {
			t.Fatal("idempotency record deleted")
		}
	}
}
