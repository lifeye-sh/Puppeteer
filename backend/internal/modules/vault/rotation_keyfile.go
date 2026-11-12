package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"io"
	"os"
	"path"
)

// KeyFileRotation holds a cooperative remote lock from preflight through commit.
// The caller must close it even if verification fails. Database account locks
// independently prevent another platform worker from replaying an unknown write.
type KeyFileRotation struct {
	client           *sftp.Client
	file, lock       string
	original, staged []byte
	oldKey, newKey   string
}

func PrepareKeyFileRotation(client *ssh.Client, oldKey, newKey string) (*KeyFileRotation, error) {
	c, e := sftp.NewClient(client)
	if e != nil {
		return nil, ErrRotationPreflight
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	if _, supported := c.HasExtension("posix-rename@openssh.com"); !supported {
		return nil, ErrRotationPreflight
	}
	home, e := c.Getwd()
	if e != nil || !path.IsAbs(home) {
		return nil, ErrRotationPreflight
	}
	dir := path.Join(home, ".ssh")
	info, e := c.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, ErrRotationPreflight
	}
	v := &KeyFileRotation{client: c, file: path.Join(dir, "authorized_keys"), lock: path.Join(dir, ".next-terminal-rotation.lock"), oldKey: oldKey, newKey: newKey}
	if e = c.Mkdir(v.lock); e != nil {
		return nil, ErrRotationPreflight
	}
	defer func() {
		if !ok {
			c.RemoveDirectory(v.lock)
		}
	}()
	if e = c.Chmod(v.lock, 0700); e != nil {
		return nil, ErrRotationPreflight
	}
	v.original, e = v.read()
	if e != nil {
		return nil, e
	}
	v.staged, e = ReplaceAuthorizedKey(v.original, oldKey, newKey, false)
	if e != nil {
		return nil, e
	}
	ok = true
	return v, nil
}
func (v *KeyFileRotation) read() ([]byte, error) {
	info, e := v.client.Lstat(v.file)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 1<<20 {
		return nil, ErrRotationPreflight
	}
	f, e := v.client.Open(v.file)
	if e != nil {
		return nil, ErrRotationPreflight
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return nil, ErrRotationPreflight
	}
	return raw, nil
}
func (v *KeyFileRotation) replace(expected, value []byte) error {
	current, e := v.read()
	if e != nil || !bytes.Equal(current, expected) {
		return ErrRotationUnknown
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return ErrRotationUnknown
	}
	tmp := path.Join(v.lock, hex.EncodeToString(nonce))
	f, e := v.client.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return ErrRotationUnknown
	}
	defer v.client.Remove(tmp)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return ErrRotationUnknown
	}
	_, e = f.Write(value)
	closed := f.Close()
	if e != nil || closed != nil {
		return ErrRotationUnknown
	}
	// Recheck immediately before atomic rename; concurrent external administrators
	// must honor the cooperative lock while a platform rotation is in progress.
	current, e = v.read()
	if e != nil || !bytes.Equal(current, expected) {
		return ErrRotationUnknown
	}
	if e = v.client.PosixRename(tmp, v.file); e != nil {
		return ErrRotationUnknown
	}
	return nil
}
func (v *KeyFileRotation) Stage() error { return v.replace(v.original, v.staged) }

// Commit may only be called after authenticating a separate connection with the
// candidate key. It removes precisely the prior key, preserving all other lines.
func (v *KeyFileRotation) Commit() error {
	value, e := ReplaceAuthorizedKey(v.staged, v.oldKey, v.newKey, true)
	if e != nil {
		return e
	}
	return v.replace(v.staged, value)
}
func (v *KeyFileRotation) Close() { _ = v.client.RemoveDirectory(v.lock); _ = v.client.Close() }
