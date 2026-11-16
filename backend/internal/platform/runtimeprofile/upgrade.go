package runtimeprofile

import (
	"errors"
	"os"
)

// UpgradeHeld fails closed, including inaccessible files and dangling symlinks.
// The deployment executor owns this marker on a read-only container mount.
func UpgradeHeld() bool {
	name := os.Getenv("NT_UPGRADE_HOLD")
	if name == "" {
		return false
	}
	_, err := os.Lstat(name)
	return !errors.Is(err, os.ErrNotExist)
}
