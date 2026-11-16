package deploytask

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type upgradeGuardFixture struct {
	fakeDriver
	blocked bool
}

func (d *upgradeGuardFixture) DeploymentBlocked() bool { return d.blocked }

func TestUpgradeBlocksAllDeploymentEntryPoints(t *testing.T) {
	d := &upgradeGuardFixture{blocked: true}
	e, err := Open(context.Background(), t.TempDir(), d)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Protocol: "mysql", Enabled: true, Bind: "127.0.0.1", Port: 13306}
	if _, err = e.Start(context.Background(), r); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if err = e.Preflight(context.Background(), r); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	called := false
	if err = e.Maintain(context.Background(), func(context.Context) error { called = true; return nil }); !errors.Is(err, ErrBusy) || called {
		t.Fatal("maintenance bypassed upgrade", err)
	}
	if len(d.calls) != 0 {
		t.Fatal("host modified while upgrade blocked", d.calls)
	}
	d.blocked = false
	if err = e.Preflight(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestHostGuardUnreadableJournal(t *testing.T) {
	d := &HostDriver{Executor: ComposeExecutor{Directory: t.TempDir()}}
	if d.DeploymentBlocked() {
		t.Fatal("no upgrade blocks deployment")
	}
	dir := filepath.Join(d.Executor.Directory, "config", "upgrade-state")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "upgrade.json"), []byte("corrupted"), 0600); e != nil {
		t.Fatal(e)
	}
	if !d.DeploymentBlocked() {
		t.Fatal("corrupt upgrade journal did not fail closed")
	}
}
