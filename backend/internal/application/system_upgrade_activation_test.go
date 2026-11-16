package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"puppeteer/backend/internal/platform/runtimeprofile"
)

func configureUpgradeActivation(t *testing.T, a *App) string {
	t.Helper()
	dir := t.TempDir()
	hold := filepath.Join(dir, "hold")
	if e := os.WriteFile(hold, nil, 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("NT_UPGRADE_HOLD", hold)
	t.Setenv("NT_RUNTIME_PROFILE", filepath.Join(dir, "active.json"))
	t.Setenv("NT_DATABASE_URL", "postgres://fixture@postgres/fixture?sslmode=disable")
	t.Setenv("NT_MFA_KEY", hex.EncodeToString(a.config.EncryptionKey))
	t.Setenv("NT_VAULT_KEY", hex.EncodeToString(a.config.VaultKey))
	a.config.DatabaseURL = os.Getenv("NT_DATABASE_URL")
	a.config.StartFrozen = true
	a.config.UpgradeStartup = true
	return hold
}

func TestUpgradeActivationProfileAndHold(t *testing.T) {
	a := &App{config: Config{EncryptionKey: bytes.Repeat([]byte{42}, 32), VaultKey: bytes.Repeat([]byte{43}, 32)}}
	hold := configureUpgradeActivation(t, a)
	if a.activationReady() {
		t.Fatal("hold ignored")
	}
	if e := os.Remove(hold); e != nil {
		t.Fatal(e)
	}
	if !a.activationReady() {
		t.Fatal("initial installation cannot activate without override")
	}
	a.config.UpgradeStartup = false
	if a.activationReady() {
		t.Fatal("restore must not fall back to environment")
	}
	a.config.UpgradeStartup = true
	p, e := runtimeprofile.FromEnvironment()
	if e != nil {
		t.Fatal(e)
	}
	p.WritesEnabled = false
	if e = runtimeprofile.Write(os.Getenv("NT_RUNTIME_PROFILE"), p); e != nil {
		t.Fatal(e)
	}
	if a.activationReady() {
		t.Fatal("restore hold bypassed by upgrade")
	}
	p.WritesEnabled = true
	p.RestoreID = "another-restore"
	if e = runtimeprofile.Write(os.Getenv("NT_RUNTIME_PROFILE"), p); e != nil {
		t.Fatal(e)
	}
	if a.activationReady() {
		t.Fatal("wrong runtime selected")
	}
	p.RestoreID = ""
	p.VaultKey = hex.EncodeToString(bytes.Repeat([]byte{44}, 32))
	if e = runtimeprofile.Write(os.Getenv("NT_RUNTIME_PROFILE"), p); e != nil {
		t.Fatal(e)
	}
	if a.activationReady() {
		t.Fatal("wrong keyring accepted")
	}
	if e = os.WriteFile(os.Getenv("NT_RUNTIME_PROFILE"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if a.activationReady() {
		t.Fatal("invalid runtime fell back to environment")
	}
}

func TestUpgradeFrozenStartupAndActivation(t *testing.T) {
	p, a, srv := r02Server(t, false)
	hold := configureUpgradeActivation(t, a)
	a.maintenance.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = a.pauseBackground(drain)
	}()
	a.StartOperationalServices(ctx)
	expect(t, call(t, srv.URL, "GET", "/api/branding", nil, nil, origin), 503)
	ready := object(t, call(t, srv.URL, "GET", "/readyz", nil, nil, origin))
	if ready["frozen"] != true || ready["upgradeHold"] != true || ready["version"] != Release {
		t.Fatal(ready)
	}
	// Observe more than one activation tick, rather than only the initial state.
	time.Sleep(1200 * time.Millisecond)
	a.backgroundMu.Lock()
	workers := a.backgroundDone != nil
	a.backgroundMu.Unlock()
	if workers || !a.maintenance.Load() {
		t.Fatal("workers or writes started before commit")
	}
	var recipient string
	if e := p.QueryRow(ctx, "SELECT recipient FROM backup_config").Scan(&recipient); e != nil {
		t.Fatal(e)
	}
	if recipient != "" {
		t.Fatal("initialization wrote before commit")
	}
	if e := os.Remove(hold); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(8 * time.Second)
	for a.maintenance.Load() && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if a.maintenance.Load() {
		t.Fatal("committed deployment did not activate")
	}
	expect(t, call(t, srv.URL, "GET", "/api/branding", nil, nil, origin), 200)
	if e := p.QueryRow(ctx, "SELECT recipient FROM backup_config").Scan(&recipient); e != nil || recipient == "" {
		t.Fatal("backup recovery initialization missed after activation", e)
	}
}
