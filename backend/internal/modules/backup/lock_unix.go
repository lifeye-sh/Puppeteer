//go:build linux || darwin || freebsd

package backup

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func AcquireRestoreLock(parent string) (func(), error) {
	if e := os.MkdirAll(parent, 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(parent, ".restore.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("another restore operation is running")
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
