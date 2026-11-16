//go:build linux

package systemupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeProcessLock(t *testing.T) {
	dir := t.TempDir()
	unlock, e := LockTransaction(dir)
	if e != nil {
		t.Fatal(e)
	}
	if release, e := LockTransaction(dir); !errors.Is(e, ErrUpgradeBusy) {
		if release != nil {
			release()
		}
		t.Fatal("lock admitted second runner", e)
	}
	unlock()
	unlock, e = LockTransaction(dir)
	if e != nil {
		t.Fatal(e)
	}
	unlock()
	if e = os.Remove(filepath.Join(dir, ".upgrade.lock")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(filepath.Join(dir, "unrelated"), filepath.Join(dir, ".upgrade.lock")); e != nil {
		t.Fatal(e)
	}
	if release, e := LockTransaction(dir); e == nil {
		release()
		t.Fatal("followed symlink lock")
	}
}
