package deploytask

import (
	"path/filepath"
	"puppeteer/backend/internal/platform/systemupdate"
)

// All deployment work, including scheduled component maintenance, observes the
// upgrade journal. An interrupted upgrade remains exclusive after daemon restart.
func (d *HostDriver) DeploymentBlocked() bool {
	if d.Executor.Directory == "" {
		return false
	}
	return systemupdate.UpgradeBlocked(filepath.Join(d.Executor.Directory, "config", "upgrade-state"))
}
