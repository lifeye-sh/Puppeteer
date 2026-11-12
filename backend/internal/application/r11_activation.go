package application

import (
	"context"
	"encoding/hex"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"os"
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
				p, e := runtimeprofile.Read(os.Getenv("NT_RUNTIME_PROFILE"))
				if e != nil || !p.WritesEnabled || p.RestoreID != a.config.RestoreID || p.DatabaseURL != a.config.DatabaseURL || p.MFAKey != hex.EncodeToString(a.config.EncryptionKey) || p.VaultKey != hex.EncodeToString(a.config.VaultKey) {
					continue
				}
				start()
				a.maintenance.Store(false)
				return
			}
		}
	}()
}
