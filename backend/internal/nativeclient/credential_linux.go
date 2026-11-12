package nativeclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const secretServicePath dbus.ObjectPath = "/org/freedesktop/secrets"
const secretServiceInterface = "org.freedesktop.Secret.Service"
const secretCollectionInterface = "org.freedesktop.Secret.Collection"
const secretItemInterface = "org.freedesktop.Secret.Item"

type linuxCredentialStore struct{ key string }
type secretServiceSlot struct {
	ctx                 context.Context
	conn                *dbus.Conn
	owner               string
	collection, session dbus.ObjectPath
	attrs               map[string]string
}
type serviceSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

func (c *Client) SystemCredentialStore() (CredentialStore, error) {
	if os.Getuid() != os.Geteuid() {
		return nil, ErrCredentialUnavailable
	}
	// Never auto-launch a bus, connect to a network bus, or use a file store.
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if _, e := secretBusAddress(address); e != nil {
		return nil, ErrCredentialUnavailable
	}
	sum := sha256.Sum256([]byte(c.origin))
	return &linuxCredentialStore{key: hex.EncodeToString(sum[:])}, nil
}

func secretBusAddress(address string) (string, error) {
	if !strings.HasPrefix(address, "unix:") || strings.Contains(address, ";") {
		return "", ErrCredentialUnavailable
	}
	var socket string
	seen := map[string]bool{}
	for _, field := range strings.Split(strings.TrimPrefix(address, "unix:"), ",") {
		key, raw, ok := strings.Cut(field, "=")
		if !ok || seen[key] {
			return "", ErrCredentialUnavailable
		}
		seen[key] = true
		value, e := dbus.UnescapeBusAddressValue(raw)
		if e != nil || value == "" || strings.ContainsRune(value, 0) {
			return "", ErrCredentialUnavailable
		}
		switch key {
		case "path":
			if socket != "" || !strings.HasPrefix(value, "/") {
				return "", ErrCredentialUnavailable
			}
			socket = value
		case "abstract":
			if socket != "" {
				return "", ErrCredentialUnavailable
			}
			socket = "@" + value
		case "guid":
		default:
			return "", ErrCredentialUnavailable
		}
	}
	if socket == "" {
		return "", ErrCredentialUnavailable
	}
	return socket, nil
}

// A stable per-UID runtime directory serializes clients across desktop sessions.
// These files contain no secrets and must not be removed while in use.
func linuxCredentialLock(ctx context.Context, key string) (func(), error) {
	if len(key) != 64 || strings.ContainsAny(key, "/\\") {
		return nil, ErrCredentialUnavailable
	}
	root, e := unix.Open(fmt.Sprintf("/run/user/%d", os.Getuid()), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, ErrCredentialUnavailable
	}
	defer unix.Close(root)
	checkDir := func(fd int) bool {
		var st unix.Stat_t
		return unix.Fstat(fd, &st) == nil && st.Uid == uint32(os.Getuid()) && st.Mode&0777 == 0700
	}
	if !checkDir(root) {
		return nil, ErrCredentialUnavailable
	}
	if e = unix.Mkdirat(root, "next-terminal-native", 0700); e != nil && !errors.Is(e, unix.EEXIST) {
		return nil, ErrCredentialUnavailable
	}
	dir, e := unix.Openat(root, "next-terminal-native", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, ErrCredentialUnavailable
	}
	defer unix.Close(dir)
	if !checkDir(dir) {
		return nil, ErrCredentialUnavailable
	}
	fd, e := unix.Openat(dir, key+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, ErrCredentialUnavailable
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0777 != 0600 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		unix.Close(fd)
		return nil, ErrCredentialUnavailable
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		if e = wait.Err(); e != nil {
			unix.Close(fd)
			return nil, e
		}
		e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
		}
		if !errors.Is(e, unix.EWOULDBLOCK) && !errors.Is(e, unix.EINTR) {
			unix.Close(fd)
			return nil, ErrCredentialUnavailable
		}
		select {
		case <-wait.Done():
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *linuxCredentialStore) WithLock(ctx context.Context, fn func(SecretSlot) error) error {
	unlock, e := linuxCredentialLock(ctx, s.key)
	if e != nil {
		return e
	}
	defer unlock()
	address, e := secretBusAddress(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if e != nil {
		return e
	}
	// Bound the handshake as well as method calls. No shared process-global bus.
	busCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	raw, e := dialer.DialContext(ctx, "unix", address)
	if e != nil {
		return ErrCredentialUnavailable
	}
	uconn := raw.(*net.UnixConn)
	control, e := uconn.SyscallConn()
	if e != nil {
		_ = uconn.Close()
		return ErrCredentialUnavailable
	}
	var peer *unix.Ucred
	var peerErr error
	e = control.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if e != nil || peerErr != nil || peer == nil || peer.Uid != uint32(os.Getuid()) {
		_ = uconn.Close()
		return ErrCredentialUnavailable
	}
	_ = uconn.SetDeadline(time.Now().Add(5 * time.Second))
	conn, e := dbus.ConnectUnix(uconn, dbus.WithContext(busCtx))
	if e != nil {
		_ = uconn.Close()
		return ErrCredentialUnavailable
	}
	_ = uconn.SetDeadline(time.Time{})
	defer conn.Close()
	v := &secretServiceSlot{ctx: ctx, conn: conn, attrs: map[string]string{"application": "next-terminal", "profile": "native-v1", "origin-sha256": s.key}}
	if e = v.call(dbus.ObjectPath("/org/freedesktop/DBus"), "org.freedesktop.DBus.GetNameOwner", "org.freedesktop.secrets").Store(&v.owner); e != nil {
		return ErrCredentialUnavailable
	}
	if !strings.HasPrefix(v.owner, ":") {
		return ErrCredentialUnavailable
	}
	if e = v.call(secretServicePath, secretServiceInterface+".ReadAlias", "default").Store(&v.collection); e != nil || v.collection == "/" {
		return ErrCredentialUnavailable
	}
	if e = v.unlocked(); e != nil {
		return e
	}
	var output dbus.Variant
	if e = v.call(secretServicePath, secretServiceInterface+".OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &v.session); e != nil || v.session == "/" || output.Value() != "" {
		return ErrCredentialUnavailable
	}
	defer v.call(v.session, "org.freedesktop.Secret.Session.Close")
	return fn(v)
}
func (s *secretServiceSlot) call(path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	destination := s.owner
	if destination == "" {
		destination = "org.freedesktop.DBus"
	}
	return s.conn.Object(destination, path).CallWithContext(ctx, method, 0, args...)
}
func (s *secretServiceSlot) unlocked() error {
	var value dbus.Variant
	if e := s.call(s.collection, "org.freedesktop.DBus.Properties.Get", secretCollectionInterface, "Locked").Store(&value); e != nil {
		return ErrCredentialUnavailable
	}
	locked, ok := value.Value().(bool)
	if !ok || locked {
		return errors.New("Secret Service collection is locked; unlock it in your desktop keyring and retry")
	}
	return nil
}
func (s *secretServiceSlot) item() (dbus.ObjectPath, error) {
	if e := s.unlocked(); e != nil {
		return "", e
	}
	var items []dbus.ObjectPath
	if e := s.call(s.collection, secretCollectionInterface+".SearchItems", s.attrs).Store(&items); e != nil {
		return "", ErrCredentialUnavailable
	}
	if len(items) == 0 {
		return "", ErrCredentialMissing
	}
	if len(items) != 1 {
		return "", errors.New("duplicate native login items in Secret Service; resolve them in your desktop keyring")
	}
	return items[0], nil
}
func (s *secretServiceSlot) Read() ([]byte, error) {
	item, e := s.item()
	if e != nil {
		return nil, e
	}
	var secret serviceSecret
	if e = s.call(item, secretItemInterface+".GetSecret", s.session).Store(&secret); e != nil {
		return nil, ErrCredentialUnavailable
	}
	defer clear(secret.Value)
	// GNOME Keyring returns text/plain even for an octet-stream CreateItem.
	// The caller validates the bounded JSON record independently of its MIME type.
	if secret.Session != s.session || len(secret.Parameters) != 0 || len(secret.Value) == 0 || len(secret.Value) > 2560 || (secret.ContentType != "application/octet-stream" && secret.ContentType != "text/plain") {
		return nil, errors.New("invalid Secret Service credential")
	}
	return append([]byte(nil), secret.Value...), nil
}
func (s *secretServiceSlot) Write(b []byte) error {
	if len(b) == 0 || len(b) > 2560 {
		return errors.New("system credential exceeds supported size")
	}
	item, e := s.item()
	secret := serviceSecret{Session: s.session, Parameters: []byte{}, Value: b, ContentType: "application/octet-stream"}
	if e == nil {
		if s.call(item, secretItemInterface+".SetSecret", secret).Err != nil {
			return ErrCredentialUnavailable
		}
		return nil
	}
	if !errors.Is(e, ErrCredentialMissing) {
		return e
	}
	props := map[string]dbus.Variant{secretItemInterface + ".Label": dbus.MakeVariant("Puppeteer native login"), secretItemInterface + ".Attributes": dbus.MakeVariant(s.attrs)}
	var path, prompt dbus.ObjectPath
	if e = s.call(s.collection, secretCollectionInterface+".CreateItem", props, secret, true).Store(&path, &prompt); e != nil || prompt != "/" || path == "/" {
		return ErrCredentialUnavailable
	}
	return nil
}
func (s *secretServiceSlot) Delete() error {
	item, e := s.item()
	if errors.Is(e, ErrCredentialMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	var prompt dbus.ObjectPath
	if e = s.call(item, secretItemInterface+".Delete").Store(&prompt); e != nil || prompt != "/" {
		return ErrCredentialUnavailable
	}
	return nil
}
