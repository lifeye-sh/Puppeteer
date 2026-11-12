package nativeclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var credentialDLL = windows.NewLazySystemDLL("advapi32.dll")
var credRead = credentialDLL.NewProc("CredReadW")
var credWrite = credentialDLL.NewProc("CredWriteW")
var credDelete = credentialDLL.NewProc("CredDeleteW")
var credFree = credentialDLL.NewProc("CredFree")

// CREDENTIALW layout follows wincred.h; only CRED_TYPE_GENERIC is used.
type winCredential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	TargetAlias, UserName   *uint16
}
type windowsCredentialStore struct{ target, mutex string }

func (c *Client) SystemCredentialStore() (CredentialStore, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, ErrCredentialUnavailable
	}
	sum := sha256.Sum256([]byte(c.origin))
	key := hex.EncodeToString(sum[:])
	// Global mutex covers multiple Windows logon sessions for the same SID.
	// It contains no credential, is not an authorization boundary, and defaults
	// to the creator's security descriptor. An inaccessible mutex fails closed.
	return &windowsCredentialStore{target: "NextTerminal/native-v1/" + key, mutex: "Global\\NextTerminal-native-v1-" + u.User.Sid.String() + "-" + key}, nil
}

func (s *windowsCredentialStore) WithLock(ctx context.Context, fn func(SecretSlot) error) error {
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	name, e := windows.UTF16PtrFromString(s.mutex)
	if e != nil {
		return ErrCredentialUnavailable
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	h, e := windows.CreateMutex(nil, false, name)
	if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return ErrCredentialUnavailable
	}
	defer windows.CloseHandle(h)
	for {
		if e := waitCtx.Err(); e != nil {
			return e
		}
		result, e := windows.WaitForSingleObject(h, 100)
		if e != nil {
			return ErrCredentialUnavailable
		}
		if result == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED {
			return ErrCredentialUnavailable
		}
		// The on-disk pending marker, not mutex abandonment alone, decides
		// whether a refresh can proceed after process failure.
		defer windows.ReleaseMutex(h)
		if e := ctx.Err(); e != nil {
			return e
		}
		return fn(s)
	}
}
func (s *windowsCredentialStore) Read() ([]byte, error) {
	name, e := windows.UTF16PtrFromString(s.target)
	if e != nil {
		return nil, ErrCredentialUnavailable
	}
	var c *winCredential
	ok, _, e := credRead.Call(uintptr(unsafe.Pointer(name)), 1, 0, uintptr(unsafe.Pointer(&c)))
	runtime.KeepAlive(name)
	if ok == 0 {
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return nil, ErrCredentialMissing
		}
		return nil, ErrCredentialUnavailable
	}
	defer credFree.Call(uintptr(unsafe.Pointer(c)))
	if c == nil || c.Type != 1 || c.BlobSize == 0 || c.BlobSize > 2560 || c.Blob == nil {
		return nil, errors.New("invalid system credential")
	}
	b := unsafe.Slice(c.Blob, int(c.BlobSize))
	out := append([]byte(nil), b...)
	clear(b)
	return out, nil
}
func (s *windowsCredentialStore) Write(b []byte) error {
	if len(b) == 0 || len(b) > 2560 {
		return errors.New("system credential exceeds supported size")
	}
	name, e := windows.UTF16PtrFromString(s.target)
	if e != nil {
		return ErrCredentialUnavailable
	}
	user, _ := windows.UTF16PtrFromString("Puppeteer native client")
	c := winCredential{Type: 1, TargetName: name, BlobSize: uint32(len(b)), Blob: &b[0], Persist: 2, UserName: user}
	ok, _, _ := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0)
	runtime.KeepAlive(c)
	runtime.KeepAlive(b)
	if ok == 0 {
		return ErrCredentialUnavailable
	}
	return nil
}
func (s *windowsCredentialStore) Delete() error {
	name, e := windows.UTF16PtrFromString(s.target)
	if e != nil {
		return ErrCredentialUnavailable
	}
	ok, _, e := credDelete.Call(uintptr(unsafe.Pointer(name)), 1, 0)
	runtime.KeepAlive(name)
	if ok == 0 && !errors.Is(e, windows.ERROR_NOT_FOUND) {
		return ErrCredentialUnavailable
	}
	return nil
}
