package nativeclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type testSecretStore struct {
	mu                sync.Mutex
	b                 []byte
	writes, failWrite int
	failDelete        bool
}

func (s *testSecretStore) WithLock(ctx context.Context, f func(SecretSlot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	return f(s)
}
func (s *testSecretStore) Read() ([]byte, error) {
	if s.b == nil {
		return nil, ErrCredentialMissing
	}
	return append([]byte(nil), s.b...), nil
}
func (s *testSecretStore) Write(b []byte) error {
	s.writes++
	if s.writes == s.failWrite {
		return errors.New("fixture write failure")
	}
	s.b = append([]byte(nil), b...)
	return nil
}
func (s *testSecretStore) Delete() error {
	if s.failDelete {
		return errors.New("fixture delete failure")
	}
	clear(s.b)
	s.b = nil
	return nil
}

const fixtureDevice = "00000000-0000-4000-8000-000000000001"

func fixtureRefresh(n int) string { return fmt.Sprintf("%064x", n) }
func seedRemembered(t *testing.T, c *Client, s *testSecretStore, scope string) {
	t.Helper()
	if e := writeSaved(s, savedLogin{Version: 1, Origin: c.origin, DeviceID: fixtureDevice, Scope: scope, Refresh: fixtureRefresh(1)}); e != nil {
		t.Fatal(e)
	}
}

func TestRememberedConcurrentRotation(t *testing.T) {
	var mu sync.Mutex
	n := 1
	revoked := false
	scope := "assets:read sessions:connect offline_access"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("client_id") != ClientID {
			http.Error(w, "bad", 400)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			revoked = true
			w.Write([]byte(`{}`))
			return
		}
		if revoked || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != fixtureRefresh(n) {
			http.Error(w, "refresh replay", 400)
			return
		}
		n++
		json.NewEncoder(w).Encode(Token{Access: fixtureRefresh(n + 1000), Refresh: fixtureRefresh(n), Type: "Bearer", Expires: 300, Scope: scope, DeviceID: fixtureDevice})
	}))
	defer server.Close()
	c, _ := New(server.URL, server.Client().Transport)
	s := &testSecretStore{}
	seedRemembered(t, c, s, scope)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := c.RememberedToken(context.Background(), s, true)
			if e != nil {
				t.Error(e)
			} else if !validOpaque(token.Access) {
				t.Error("no access token")
			}
		}()
	}
	wg.Wait()
	if n != 41 {
		t.Fatalf("rotations %d", n-1)
	}
	v, e := readSaved(s, c.origin)
	if e != nil || v.Pending || v.Refresh != fixtureRefresh(41) {
		t.Fatal("last rotation was not saved", e)
	}
	if strings.Contains(string(s.b), "access_token") {
		t.Fatal("access token persisted")
	}
	if e := c.ForgetLogin(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if !revoked || s.b != nil {
		t.Fatal("logout did not clear and revoke")
	}
}

func TestRememberedFailures(t *testing.T) {
	for _, mode := range []string{"lost-response", "changed-device", "changed-scope", "reused-refresh", "bad-access", "save-failed", "marker-failed", "pending", "pending-offline", "logout-offline", "scope-denied", "wrong-origin", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			var refreshes, revokes atomic.Int32
			scope := "assets:read offline_access"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/revoke") {
					revokes.Add(1)
					if strings.HasSuffix(mode, "offline") {
						http.Error(w, "offline", 503)
					} else {
						w.Write([]byte(`{}`))
					}
					return
				}
				refreshes.Add(1)
				if mode == "lost-response" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				v := Token{Access: fixtureRefresh(9), Refresh: fixtureRefresh(2), Type: "Bearer", Expires: 300, Scope: scope, DeviceID: fixtureDevice}
				switch mode {
				case "changed-device":
					v.DeviceID = "00000000-0000-4000-8000-000000000002"
				case "changed-scope":
					v.Scope += " sessions:connect"
				case "reused-refresh":
					v.Refresh = fixtureRefresh(1)
				case "bad-access":
					v.Access = "bad"
				}
				json.NewEncoder(w).Encode(v)
			}))
			defer server.Close()
			c, _ := New(server.URL, server.Client().Transport)
			s := &testSecretStore{}
			seedRemembered(t, c, s, scope)
			switch mode {
			case "save-failed":
				s.failWrite = 3
			case "marker-failed":
				s.failWrite = 2
			case "pending", "pending-offline":
				v, _ := readSaved(s, c.origin)
				v.Pending = true
				_ = writeSaved(s, v)
			case "wrong-origin":
				v, _ := readSaved(s, c.origin)
				v.Origin = "https://other.invalid"
				_ = writeSaved(s, v)
			case "corrupt":
				s.b = []byte("bad-json")
			}
			var e error
			if mode == "logout-offline" {
				e = c.ForgetLogin(context.Background(), s)
			} else {
				_, e = c.RememberedToken(context.Background(), s, mode == "scope-denied")
			}
			if e == nil {
				t.Fatal("failure accepted")
			}
			if strings.Contains(e.Error(), fixtureRefresh(1)) {
				t.Fatal("secret in error")
			}
			noRequest := mode == "marker-failed" || strings.HasPrefix(mode, "pending") || mode == "logout-offline" || mode == "scope-denied" || mode == "wrong-origin" || mode == "corrupt"
			if noRequest && refreshes.Load() != 0 {
				t.Fatal("unexpected refresh")
			}
			if mode == "pending-offline" {
				v, e := readSaved(s, c.origin)
				if e != nil || !v.Pending {
					t.Fatal("lost pending marker")
				}
				_, _ = c.RememberedToken(context.Background(), s, false)
				if refreshes.Load() != 0 {
					t.Fatal("pending refresh replayed")
				}
			}
			if mode == "lost-response" || mode == "save-failed" || mode == "pending" || mode == "logout-offline" || strings.HasPrefix(mode, "changed-") || mode == "reused-refresh" || mode == "bad-access" {
				if s.b != nil || revokes.Load() != 1 {
					t.Fatal("failure did not revoke and clear")
				}
			}
		})
	}
}

func TestRememberedStoreBoundary(t *testing.T) {
	c, _ := New("https://platform.invalid", nil)
	s := &testSecretStore{}
	if _, e := c.RememberedToken(context.Background(), s, false); !errors.Is(e, ErrCredentialMissing) {
		t.Fatal(e)
	}
	seedRemembered(t, c, s, "assets:read offline_access")
	opened := false
	if e := c.RememberLogin(context.Background(), s, func(string) error { opened = true; return nil }, false); e == nil || opened {
		t.Fatal("replaced existing login")
	}
	s.failDelete = true
	if e := c.ForgetLogin(context.Background(), s); e == nil || s.b == nil {
		t.Fatal("ignored local delete failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.RememberedToken(ctx, s, false); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
