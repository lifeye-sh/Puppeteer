//go:build linux

package hostupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/systemupdate"
)

type pausedVerification struct {
	*Driver
	marker string
}

func (d *pausedVerification) Verify(context.Context, systemupdate.UpgradePlan, systemupdate.Release) error {
	if e := os.WriteFile(d.marker, []byte("frozen candidate ready"), 0600); e != nil {
		return e
	}
	for {
		time.Sleep(time.Hour)
	}
}

// Used only by tests/host_upgrade_integration.py in its unique systemd sandbox.
// The compiled test binary uses a generated fixture trust root via go -overlay.
func TestHostUpgradeIntegrationHelper(t *testing.T) {
	root := os.Getenv("PUPPETEER_UPGRADE_FIXTURE_ROOT")
	if root == "" {
		t.Skip("isolated host fixture required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("fixture requires root inside its isolated systemd container")
	}
	raw, e := os.ReadFile(filepath.Join(root, "config", "deployd.json"))
	if e != nil {
		t.Fatal(e)
	}
	var cfg struct {
		Executor deploytask.ComposeExecutor `json:"executor"`
	}
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg.Executor.Directory != root {
		t.Fatal("fixture root mismatch")
	}
	unlock, e := systemupdate.LockTransaction(root)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	dir := filepath.Join(root, "config", "upgrade-state")
	if os.Getenv("PUPPETEER_UPGRADE_FIXTURE_ACTION") == "create" {
		envelope, e := os.ReadFile(filepath.Join(root, "fixture-index.json"))
		if e != nil {
			t.Fatal(e)
		}
		layout, e := os.ReadFile(filepath.Join(root, "compose-managed.json"))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(layout)
		_, e = systemupdate.CreateTransaction(dir, systemupdate.UpgradePlan{ID: os.Getenv("PUPPETEER_UPGRADE_FIXTURE_ID"), Actor: os.Getenv("PUPPETEER_UPGRADE_FIXTURE_ACTOR"), Source: "beta0.10", Target: "beta0.11", LayoutDigest: hex.EncodeToString(sum[:]), Envelope: envelope}, systemupdate.TrustedKeys())
		if e != nil {
			t.Fatal(e)
		}
		return
	}
	tx, e := systemupdate.OpenTransaction(dir, systemupdate.TrustedKeys())
	if e != nil {
		t.Fatal(e)
	}
	driver := &Driver{Executor: cfg.Executor}
	var operation systemupdate.UpgradeDriver = driver
	if os.Getenv("PUPPETEER_UPGRADE_FIXTURE_PAUSE") == "true" {
		operation = &pausedVerification{Driver: driver, marker: filepath.Join(dir, "verification-paused")}
	}
	e = tx.Run(context.Background(), operation)
	if e != nil && !tx.View().Terminal() && tx.View().Stage != "recovery_required" {
		t.Fatal(e)
	}
}
