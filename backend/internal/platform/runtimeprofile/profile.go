// Package runtimeprofile atomically selects a database and its matching keyring.
// Profiles are private deployment files, never returned through HTTP or logs.
package runtimeprofile

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

type Profile struct {
	DatabaseURL   string `json:"databaseUrl"`
	MFAKey        string `json:"mfaKey"`
	VaultKey      string `json:"vaultKey"`
	RestoreID     string `json:"restoreId"`
	WritesEnabled bool   `json:"writesEnabled"`
}
type Storage struct {
	DatabaseURL string `json:"databaseUrl"`
	RestoreID   string `json:"restoreId"`
}

func (p Profile) Validate() error {
	u, e := url.Parse(p.DatabaseURL)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("invalid runtime database profile")
	}
	for _, v := range []string{p.MFAKey, p.VaultKey} {
		raw, e := hex.DecodeString(v)
		if e != nil || len(raw) != 32 {
			return errors.New("invalid runtime key profile")
		}
		clear(raw)
	}
	return nil
}
func Read(path string) (Profile, error) {
	var p Profile
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil {
		return p, e
	}
	defer clear(raw)
	if len(raw) > 16384 || json.Unmarshal(raw, &p) != nil {
		return p, errors.New("invalid runtime profile")
	}
	return p, p.Validate()
}
func FromEnvironment() (Profile, error) {
	if path := os.Getenv("NT_RUNTIME_PROFILE"); path != "" {
		p, e := Read(path)
		if e == nil {
			return p, nil
		}
		if !os.IsNotExist(e) {
			return p, e
		}
	}
	p := Profile{DatabaseURL: os.Getenv("NT_DATABASE_URL"), MFAKey: os.Getenv("NT_MFA_KEY"), VaultKey: os.Getenv("NT_VAULT_KEY"), WritesEnabled: true}
	return p, p.Validate()
}
func Apply() error {
	p, e := FromEnvironment()
	if e != nil {
		return e
	}
	for k, v := range map[string]string{"NT_DATABASE_URL": p.DatabaseURL, "NT_MFA_KEY": p.MFAKey, "NT_VAULT_KEY": p.VaultKey} {
		if e = os.Setenv(k, v); e != nil {
			return e
		}
	}
	return nil
}
func AtomicJSON(path string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	defer clear(raw)
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".profile-attempt-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func Write(path string, p Profile) error {
	if e := p.Validate(); e != nil {
		return e
	}
	return AtomicJSON(path, p)
}
func ApplyStorage() error {
	name := os.Getenv("NT_STORAGE_PROFILE")
	if name == "" {
		return nil
	}
	f, e := os.Open(name)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	var s Storage
	if e = json.NewDecoder(io.LimitReader(f, 16384)).Decode(&s); e != nil || s.DatabaseURL == "" {
		return errors.New("invalid storage runtime profile")
	}
	return os.Setenv("NT_DATABASE_URL", s.DatabaseURL)
}
