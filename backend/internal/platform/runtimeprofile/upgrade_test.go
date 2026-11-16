package runtimeprofile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeHold(t *testing.T) {
	t.Setenv("NT_UPGRADE_HOLD", "")
	if UpgradeHeld() {
		t.Fatal("unconfigured deployment held")
	}
	name := filepath.Join(t.TempDir(), "hold")
	t.Setenv("NT_UPGRADE_HOLD", name)
	if UpgradeHeld() {
		t.Fatal("absent marker held")
	}
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !UpgradeHeld() {
		t.Fatal("existing marker not held")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(name+"-missing", name); err == nil {
		if !UpgradeHeld() {
			t.Fatal("dangling marker must fail closed")
		}
	}
}
