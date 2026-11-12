package backup

import (
	"errors"
	"golang.org/x/sys/windows"
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
	var overlapped windows.Overlapped
	if e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); e != nil {
		f.Close()
		return nil, errors.New("another restore operation is running")
	}
	return func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped); f.Close() }, nil
}
