package nativeclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWindowsCredentialHelper(t *testing.T) {
	origin := os.Getenv("NT_KEYRING_TEST_ORIGIN")
	if origin == "" {
		t.Skip("subprocess helper")
	}
	if !strings.HasPrefix(origin, "https://r14-keyring-test-") || !strings.HasSuffix(origin, ".invalid") {
		t.Fatal("test namespace required")
	}
	c, _ := New(origin, nil)
	s, e := c.SystemCredentialStore()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10; i++ {
		if e := s.WithLock(context.Background(), func(slot SecretSlot) error {
			b, e := slot.Read()
			if e != nil {
				return e
			}
			n, e := strconv.Atoi(string(b))
			clear(b)
			if e != nil {
				return e
			}
			time.Sleep(5 * time.Millisecond)
			return slot.Write([]byte(strconv.Itoa(n + 1)))
		}); e != nil {
			t.Fatal(e)
		}
	}
}

// Opt in: writes only a unique synthetic test credential and deletes it.
func TestWindowsCredentialStore(t *testing.T) {
	if os.Getenv("NT_TEST_SYSTEM_CREDENTIAL_STORE") != "1" {
		t.Skip("requires Windows credential-store acceptance opt-in")
	}
	origin := fmt.Sprintf("https://r14-keyring-test-%d-%d.invalid", os.Getpid(), time.Now().UnixNano())
	c, _ := New(origin, nil)
	s, e := c.SystemCredentialStore()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.WithLock(context.Background(), func(slot SecretSlot) error { return slot.Delete() }); e != nil {
			t.Error(e)
		}
	})
	if e := s.WithLock(context.Background(), func(slot SecretSlot) error {
		if _, e := slot.Read(); e != ErrCredentialMissing {
			return fmt.Errorf("test credential unexpectedly existed")
		}
		if e := slot.Write(make([]byte, 2561)); e == nil {
			return fmt.Errorf("oversized credential accepted")
		}
		return slot.Write([]byte("0"))
	}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsCredentialHelper$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "NT_KEYRING_TEST_ORIGIN="+origin)
			if output, e := cmd.CombinedOutput(); e != nil {
				t.Errorf("child failed: %v %s", e, output)
			}
		}()
	}
	wg.Wait()
	if e := s.WithLock(context.Background(), func(slot SecretSlot) error {
		b, e := slot.Read()
		if e != nil {
			return e
		}
		defer clear(b)
		if string(b) != "40" {
			return fmt.Errorf("cross-process updates were lost")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// A new store instance sees the persisted value; a different origin does not.
	other, _ := New(origin+":443", nil)
	otherStore, e := other.SystemCredentialStore()
	if e != nil {
		t.Fatal(e)
	}
	if e := otherStore.WithLock(context.Background(), func(slot SecretSlot) error {
		_, e := slot.Read()
		if e != ErrCredentialMissing {
			return fmt.Errorf("origin isolation failed")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.WithLock(context.Background(), func(SecretSlot) error { close(held); <-release; return nil })
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	e = s.WithLock(ctx, func(SecretSlot) error { return fmt.Errorf("entered locked store") })
	cancel()
	close(release)
	if e != context.DeadlineExceeded {
		t.Fatal("lock did not honor cancellation", e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
