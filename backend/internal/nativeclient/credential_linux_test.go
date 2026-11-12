package nativeclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestLinuxCredentialNoBus(t *testing.T) {
	c, _ := New("https://fixture.invalid", nil)
	for _, address := range []string{"", "tcp:host=localhost,port=1234", "unix:path=/tmp/x;tcp:host=x", "autolaunch:", "unix:path=relative", "unix:path=/tmp/x,abstract=y", "unix:path=/tmp/x,path=/tmp/y", "unix:path=%00", "unix:path=/tmp/x,unknown=y"} {
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
		if _, e := c.SystemCredentialStore(); !errors.Is(e, ErrCredentialUnavailable) {
			t.Fatal("accepted non-local or missing bus")
		}
	}
}

func TestLinuxBusAddress(t *testing.T) {
	for input, want := range map[string]string{"unix:path=/run/user/1000/bus": "/run/user/1000/bus", "unix:abstract=/tmp/dbus-x,guid=0123456789": "@/tmp/dbus-x", "unix:path=/tmp/a%2cb": "/tmp/a,b"} {
		got, e := secretBusAddress(input)
		if e != nil || got != want {
			t.Fatalf("address parse %q: %q %v", input, got, e)
		}
	}
}
func TestLinuxCredentialHelper(t *testing.T) {
	origin := os.Getenv("NT_LINUX_KEYRING_TEST_ORIGIN")
	if origin == "" {
		t.Skip("subprocess helper")
	}
	if !strings.HasPrefix(origin, "https://r14-linux-keyring-") || !strings.HasSuffix(origin, ".invalid") {
		t.Fatal("test namespace required")
	}
	c, _ := New(origin, nil)
	s, e := c.SystemCredentialStore()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		if e := s.WithLock(context.Background(), func(slot SecretSlot) error {
			b, e := slot.Read()
			if e != nil {
				return e
			}
			defer clear(b)
			n, e := strconv.Atoi(string(b))
			if e != nil {
				return e
			}
			time.Sleep(10 * time.Millisecond)
			return slot.Write([]byte(strconv.Itoa(n + 1)))
		}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestLinuxCredentialStore(t *testing.T) {
	if os.Getenv("NT_TEST_ISOLATED_KEYRING") != "1" {
		t.Skip("requires isolated disposable GNOME Keyring fixture")
	}
	if os.Getuid() != 10001 || os.Getenv("HOME") != "/home/keyring-test" {
		t.Fatal("not running in dedicated keyring-test container user")
	}
	ctx := context.Background()
	origin := fmt.Sprintf("https://r14-linux-keyring-%d-%d.invalid", os.Getpid(), time.Now().UnixNano())
	c, _ := New(origin, nil)
	store, e := c.SystemCredentialStore()
	if e != nil {
		t.Fatal(e)
	}
	s := store.(*linuxCredentialStore)
	cleaned := false
	t.Cleanup(func() {
		if cleaned {
			return
		}
		if e := s.WithLock(ctx, func(slot SecretSlot) error { return slot.Delete() }); e != nil {
			t.Error(e)
		}
	})
	if e := s.WithLock(ctx, func(slot SecretSlot) error {
		if _, e := slot.Read(); !errors.Is(e, ErrCredentialMissing) {
			return fmt.Errorf("test item unexpectedly exists: %v", e)
		}
		if e := slot.Write(make([]byte, 2561)); e == nil {
			return fmt.Errorf("oversized secret accepted")
		}
		return slot.Write([]byte("0"))
	}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	if e := s.WithLock(ctx, func(slot SecretSlot) error {
		v := slot.(*secretServiceSlot)
		item, e := v.item()
		if e != nil {
			return e
		}
		var value serviceSecret
		if e = v.call(item, secretItemInterface+".GetSecret", v.session).Store(&value); e != nil {
			return e
		}
		defer clear(value.Value)
		t.Logf("fixture secret metadata: collection=%s, session match=%t, parameters=%d, bytes=%d, type=%q", v.collection, value.Session == v.session, len(value.Parameters), len(value.Value), value.ContentType)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxCredentialHelper$", "-test.timeout=40s")
			cmd.Env = append(os.Environ(), "NT_LINUX_KEYRING_TEST_ORIGIN="+origin)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Errorf("child failed: %v %s", e, out)
			}
		}()
	}
	wg.Wait()
	if e := s.WithLock(ctx, func(slot SecretSlot) error {
		b, e := slot.Read()
		if e != nil {
			return e
		}
		defer clear(b)
		if string(b) != "20" {
			return fmt.Errorf("cross-process updates lost")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	other, _ := New(origin+":444", nil)
	otherStore, _ := other.SystemCredentialStore()
	if e := otherStore.WithLock(ctx, func(slot SecretSlot) error {
		_, e := slot.Read()
		if !errors.Is(e, ErrCredentialMissing) {
			return fmt.Errorf("origin isolation failed")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	lockPath := filepath.Join(fmt.Sprintf("/run/user/%d", os.Getuid()), "next-terminal-native", s.key+".lock")
	if e := os.Chmod(lockPath, 0644); e != nil {
		t.Fatal(e)
	}
	e = s.WithLock(ctx, func(SecretSlot) error { return errors.New("should not run") })
	if chmod := os.Chmod(lockPath, 0600); chmod != nil {
		t.Fatal(chmod)
	}
	if !errors.Is(e, ErrCredentialUnavailable) {
		t.Fatal("insecure lock accepted", e)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.WithLock(ctx, func(SecretSlot) error { close(held); <-release; return nil }) }()
	<-held
	wait, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	e = s.WithLock(wait, func(SecretSlot) error { return errors.New("should not run") })
	cancel()
	close(release)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("lock cancellation failed", e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	t.Run("rotation-in-real-keyring", testLinuxRememberRotation)
	if e := s.WithLock(ctx, func(slot SecretSlot) error {
		if e := slot.Delete(); e != nil {
			return e
		}
		_, e := slot.Read()
		if !errors.Is(e, ErrCredentialMissing) {
			return errors.New("deleted item survived")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	cleaned = true
	// Lock only after deleting fixture secrets. Automatic unlocking is not a
	// client feature; the empty, locked collection dies with this container.
	if e := s.WithLock(ctx, func(slot SecretSlot) error {
		v := slot.(*secretServiceSlot)
		var locked []dbus.ObjectPath
		var prompt dbus.ObjectPath
		return v.call(secretServicePath, secretServiceInterface+".Lock", []dbus.ObjectPath{v.collection}).Store(&locked, &prompt)
	}); e != nil {
		t.Fatal(e)
	}
	e = s.WithLock(ctx, func(SecretSlot) error { return errors.New("should not run") })
	if e == nil || !strings.Contains(e.Error(), "locked") {
		t.Fatal("locked collection not rejected", e)
	}
}

func testLinuxRememberRotation(t *testing.T) {
	var mu sync.Mutex
	rotations, revokes := 0, 0
	scope := "assets:read offline_access"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			revokes++
			return
		}
		if r.ParseForm() != nil || r.Form.Get("refresh_token") != fixtureRefresh(rotations+1) {
			http.Error(w, "replay", 400)
			return
		}
		rotations++
		json.NewEncoder(w).Encode(Token{Access: fixtureRefresh(100 + rotations), Refresh: fixtureRefresh(1 + rotations), Type: "Bearer", Expires: 300, Scope: scope, DeviceID: fixtureDevice})
	}))
	defer server.Close()
	c, _ := New(server.URL, server.Client().Transport)
	store, e := c.SystemCredentialStore()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		if e := store.WithLock(ctx, func(slot SecretSlot) error { return slot.Delete() }); e != nil {
			t.Error(e)
		}
	})
	if e := store.WithLock(ctx, func(slot SecretSlot) error {
		return writeSaved(slot, savedLogin{Version: 1, Origin: c.origin, Scope: scope, DeviceID: fixtureDevice, Refresh: fixtureRefresh(1)})
	}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e := c.RememberedToken(ctx, store, false); e != nil {
			t.Fatal(e)
		}
	}
	if e := c.ForgetLogin(ctx, store); e != nil {
		t.Fatal(e)
	}
	if e := store.WithLock(ctx, func(slot SecretSlot) error {
		if _, e := slot.Read(); !errors.Is(e, ErrCredentialMissing) {
			return errors.New("logout retained credential")
		}
		return writeSaved(slot, savedLogin{Version: 1, Origin: c.origin, Scope: scope, DeviceID: fixtureDevice, Refresh: fixtureRefresh(3), Pending: true})
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.RememberedToken(ctx, store, false); e == nil {
		t.Fatal("pending marker was ignored")
	}
	mu.Lock()
	defer mu.Unlock()
	if rotations != 2 || revokes != 2 {
		t.Fatal("pending state refreshed or failed to revoke")
	}
}
