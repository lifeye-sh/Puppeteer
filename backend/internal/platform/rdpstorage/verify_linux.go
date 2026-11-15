//go:build linux

package rdpstorage

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"strings"
)

func CheckMount() error {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return errors.New("cannot inspect storage mount")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != mountpoint {
			continue
		}
		sep := strings.Index(line, " - fuse.nt-storage nt-user-storage ")
		if sep < 0 {
			return errors.New("unexpected storage mount type")
		}
		options := "," + fields[5] + ","
		for _, option := range []string{"rw", "nosuid", "nodev", "noexec"} {
			if !strings.Contains(options, ","+option+",") {
				return errors.New("storage mount lacks required restrictions")
			}
		}
		_, err = os.Stat(mountpoint + "/.deployment-probe")
		return err
	}
	return errors.New("storage is not mounted")
}
func VerifyIO() error {
	if err := CheckMount(); err != nil {
		return err
	}
	f, err := os.OpenFile(mountpoint+"/.deployment-probe", os.O_RDWR|os.O_TRUNC, 0)
	if err != nil {
		return errors.New("storage diagnostic inode unavailable")
	}
	defer f.Close()
	data := make([]byte, 64)
	if _, err = rand.Read(data); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return errors.New("mounted storage write failed")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	got := make([]byte, len(data))
	if _, err = io.ReadFull(f, got); err != nil || !bytes.Equal(data, got) {
		return errors.New("mounted storage readback mismatch")
	}
	if err = f.Truncate(0); err != nil {
		return err
	}
	return nil
}
