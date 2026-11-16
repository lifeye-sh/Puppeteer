package application

import (
	"context"
	"encoding/hex"
	"log/slog"
	"os"
	"puppeteer/backend/internal/platform/runtimeprofile"
	"time"
)

// Restored deployments start with business routes and workers frozen. The
// offline CLI opens writes only after the new runtime passes readiness checks.
func (a *App) StartOperationalServices(ctx context.Context) {
	start := func() { a.StartBackgroundServices(ctx); go a.RunBackupMaintenance(ctx) }
	if !a.config.StartFrozen {
		start()
		return
	}
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if !a.activationReady() {
					continue
				}
				if a.config.UpgradeStartup {
					initial, cancel := context.WithTimeout(ctx, 20*time.Second)
					err := a.InitializeBackupRecovery(initial)
					if err == nil {
						err = a.RecoverFiles(initial)
					}
					cancel()
					if err != nil {
						slog.Error("upgrade activation initialization failed; business remains frozen")
						continue
					}
				}
				if ctx.Err() != nil || !a.activationReady() {
					continue
				}
				start()
				a.maintenance.Store(false)
				return
			}
		}
	}()
}

func (a *App) activationReady() bool {
	if runtimeprofile.UpgradeHeld() {
		return false
	}
	p, e := runtimeprofile.Read(os.Getenv("NT_RUNTIME_PROFILE"))
	// New installations have no runtime override until their first restore.
	// Upgrade activation may use the original environment; restore activation
	// must still wait for the explicitly committed matching runtime profile.
	if a.config.UpgradeStartup && os.IsNotExist(e) {
		p, e = runtimeprofile.FromEnvironment()
	}
	return e == nil && p.WritesEnabled && p.RestoreID == a.config.RestoreID && p.DatabaseURL == a.config.DatabaseURL && p.MFAKey == hex.EncodeToString(a.config.EncryptionKey) && p.VaultKey == hex.EncodeToString(a.config.VaultKey)
}
