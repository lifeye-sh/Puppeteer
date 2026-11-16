//go:build linux

package systemupdate

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// LockTransaction shares the installer's upgrade.lock. The lock must be held
// from Create/Open through all side effects, including recovery. CLOEXEC keeps
// subprocesses from unintentionally holding the lock after runner termination.
func LockTransaction(installation string) (func(), error) {
	if !filepath.IsAbs(installation) {
		return nil, errors.New("安装路径必须为绝对路径")
	}
	fd, e := unix.Open(filepath.Join(installation, ".upgrade.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "upgrade lock")
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, ErrUpgradeBusy
	}
	return func() { _ = f.Close() }, nil
}
