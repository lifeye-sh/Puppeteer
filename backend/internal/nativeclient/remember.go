package nativeclient

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

var ErrCredentialMissing = errors.New("no saved login; run login --remember")
var ErrCredentialUnavailable = errors.New("system credential store is unavailable; use browser login without --use-login")

// SecretSlot is accessible only during WithLock. Implementations must never
// fall back to a plain file. A lock spans read, refresh and replacement.
type SecretSlot interface {
	Read() ([]byte, error)
	Write([]byte) error
	Delete() error
}
type CredentialStore interface {
	WithLock(context.Context, func(SecretSlot) error) error
}

type savedLogin struct {
	Version  int    `json:"version"`
	Origin   string `json:"origin"`
	DeviceID string `json:"deviceId"`
	Scope    string `json:"scope"`
	Refresh  string `json:"refresh"`
	Pending  bool   `json:"pending"`
}

func validOpaque(s string) bool {
	_, e := hex.DecodeString(s)
	return len(s) == 64 && e == nil
}
func validSaved(v savedLogin, origin string) bool {
	if v.Version != 1 || v.Origin != origin || !validOpaque(v.Refresh) || len(v.DeviceID) != 36 {
		return false
	}
	scopes := strings.Fields(v.Scope)
	if !slices.Contains(scopes, "assets:read") || !slices.Contains(scopes, "offline_access") {
		return false
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		if seen[s] || (s != "assets:read" && s != "sessions:connect" && s != "offline_access") {
			return false
		}
		seen[s] = true
	}
	return true
}
func readSaved(slot SecretSlot, origin string) (savedLogin, error) {
	b, e := slot.Read()
	if e != nil {
		return savedLogin{}, e
	}
	defer clear(b)
	var v savedLogin
	if len(b) > 2560 || json.Unmarshal(b, &v) != nil || !validSaved(v, origin) {
		return savedLogin{}, errors.New("saved login is invalid or belongs to another server; clear it with logout")
	}
	return v, nil
}
func writeSaved(slot SecretSlot, v savedLogin) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	defer clear(b)
	return slot.Write(b)
}
func (c *Client) revokeSaved(refresh string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.form(ctx, "revoke", url.Values{"client_id": {ClientID}, "token": {refresh}}, nil)
}

// RememberLogin never silently replaces a previous installation's login.
// The browser consent is explicit and the system store is checked first.
func (c *Client) RememberLogin(ctx context.Context, store CredentialStore, open func(string) error, connect bool) error {
	return store.WithLock(ctx, func(slot SecretSlot) error {
		b, e := slot.Read()
		clear(b)
		if e == nil {
			return errors.New("a login is already saved for this server; logout before changing accounts or permissions")
		}
		if !errors.Is(e, ErrCredentialMissing) {
			return e
		}
		t, e := c.LoginPersistent(ctx, open, connect)
		if e != nil {
			return e
		}
		v := savedLogin{Version: 1, Origin: c.origin, DeviceID: t.DeviceID, Scope: t.Scope, Refresh: t.Refresh}
		if !validSaved(v, c.origin) || !validOpaque(t.Access) {
			if validOpaque(t.Refresh) {
				_ = c.revokeSaved(t.Refresh)
			}
			return errors.New("invalid persistent authorization response")
		}
		if e = writeSaved(slot, v); e != nil {
			if c.revokeSaved(t.Refresh) != nil {
				return errors.New("could not save login or confirm revocation; revoke this device in the personal account page")
			}
			return errors.New("could not save login; new authorization revoked")
		}
		return nil
	})
}

// RememberedToken rotates exactly once. A persisted pending marker precedes
// the request, so a crash or lost response can never replay a refresh token.
func (c *Client) RememberedToken(ctx context.Context, store CredentialStore, connect bool) (Token, error) {
	var token Token
	e := store.WithLock(ctx, func(slot SecretSlot) error {
		v, e := readSaved(slot, c.origin)
		if e != nil {
			return e
		}
		if v.Pending {
			if c.revokeSaved(v.Refresh) != nil {
				return errors.New("interrupted refresh requires revocation; restore connectivity and retry, or revoke this device in the personal account page")
			}
			if e := slot.Delete(); e != nil {
				return e
			}
			return errors.New("interrupted refresh was revoked; run login --remember again")
		}
		if connect && !slices.Contains(strings.Fields(v.Scope), "sessions:connect") {
			return errors.New("saved login has no connection permission; logout and login --remember --allow-connect")
		}
		v.Pending = true
		if e = writeSaved(slot, v); e != nil {
			return errors.New("could not secure refresh state; no request sent")
		}
		var next Token
		e = c.form(ctx, "token", url.Values{"client_id": {ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {v.Refresh}}, &next)
		if e == nil && (!validOpaque(next.Access) || !validOpaque(next.Refresh) || next.Refresh == v.Refresh || next.Type != "Bearer" || next.Expires < 1 || next.Expires > 300 || next.DeviceID != v.DeviceID || next.Scope != v.Scope) {
			e = errors.New("invalid refresh response")
		}
		if e != nil {
			// Keep the pending marker when the server cannot confirm revocation.
			if c.revokeSaved(v.Refresh) == nil {
				if slot.Delete() != nil {
					return errors.New("refresh failed; authorization revoked but local credential cleanup failed; run logout")
				}
				return errors.New("refresh failed; authorization revoked; run login --remember again")
			}
			return errors.New("refresh outcome uncertain; saved login blocked until revocation can be confirmed")
		}
		v.Refresh, v.Pending = next.Refresh, false
		if e = writeSaved(slot, v); e != nil {
			if c.revokeSaved(next.Refresh) != nil {
				return errors.New("rotated credential could not be saved; revoke this device in the personal account page")
			}
			if slot.Delete() != nil {
				return errors.New("authorization revoked but local credential cleanup failed; run logout")
			}
			return errors.New("rotated credential could not be saved; authorization revoked")
		}
		token = next
		return nil
	})
	return token, e
}

// ForgetLogin clears the local record even if the platform is unreachable.
// In that case it explicitly reports that remote revocation is unconfirmed.
func (c *Client) ForgetLogin(ctx context.Context, store CredentialStore) error {
	return store.WithLock(ctx, func(slot SecretSlot) error {
		v, e := readSaved(slot, c.origin)
		if errors.Is(e, ErrCredentialMissing) {
			return nil
		}
		if del := slot.Delete(); del != nil {
			return errors.New("could not clear system credential; login remains saved")
		}
		if e != nil {
			return errors.New("invalid local credential cleared; verify device revocation in the personal account page")
		}
		if c.revokeSaved(v.Refresh) != nil {
			return errors.New("local login cleared; remote revocation unconfirmed, revoke this device in the personal account page")
		}
		return nil
	})
}
