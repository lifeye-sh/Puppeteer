// Package deployment validates the single-host production deployment boundary.
package deployment

import (
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// LoadSecrets supports Compose file secrets without putting secret values in its model.
func LoadSecrets() error {
	for _, key := range []string{"NT_DATABASE_URL", "NT_MFA_KEY", "NT_VAULT_KEY", "NT_MP4_TOKEN", "NT_RESTORE_DATABASE_URL", "NT_RESTORE_ADMIN_URL"} {
		path := os.Getenv(key + "_FILE")
		if path == "" {
			continue
		}
		if os.Getenv(key) != "" {
			return errors.New(key + " and its _FILE alternative cannot both be set")
		}
		f, err := os.Open(path)
		if err != nil {
			return errors.New("cannot read " + key + " secret file")
		}
		b, err := io.ReadAll(io.LimitReader(f, 16385))
		f.Close()
		if err != nil || len(b) > 16384 || len(strings.TrimSpace(string(b))) == 0 {
			clear(b)
			return errors.New("invalid " + key + " secret file")
		}
		value := strings.TrimSpace(string(b))
		clear(b)
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

func Validate(get func(string) string) error {
	mode := get("NT_ENV")
	if mode == "development" {
		return nil
	}
	if mode != "production" {
		return errors.New("NT_ENV must be development or production")
	}
	origins := strings.Split(get("NT_ALLOWED_ORIGINS"), ",")
	for _, origin := range origins {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Host, "*") {
			return errors.New("production requires explicit HTTPS origins")
		}
	}
	for _, k := range []string{"NT_MFA_KEY", "NT_VAULT_KEY"} {
		b, e := hex.DecodeString(get(k))
		valid := e == nil && len(b) == 32
		clear(b)
		if !valid {
			return errors.New("production requires a 32-byte hex " + k)
		}
	}
	if get("NT_MFA_KEY") == get("NT_VAULT_KEY") {
		return errors.New("production encryption keys must be independent")
	}
	u, e := url.Parse(get("NT_DATABASE_URL"))
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
		return errors.New("invalid production database URL")
	}
	if u.Query().Get("sslmode") != "verify-full" && !(u.Query().Get("sslmode") == "disable" && u.Hostname() == "postgres" && get("NT_DATABASE_NETWORK") == "private-container") {
		return errors.New("production database requires verify-full TLS or the isolated Compose postgres service")
	}
	if get("NT_MP4_URL") != "" && len(get("NT_MP4_TOKEN")) < 32 {
		return errors.New("production renderer requires a strong shared token")
	}
	for _, k := range []string{"NT_BACKUP_DIRECTORY", "NT_RESTORE_DIRECTORY", "NT_MP4_DIRECTORY", "NT_RUNTIME_PROFILE", "NT_STORAGE_PROFILE"} {
		if !filepath.IsAbs(get(k)) {
			return errors.New("production requires an absolute persistent path for " + k)
		}
	}
	return nil
}

func CheckStorage() error {
	for _, k := range []string{"NT_BACKUP_DIRECTORY", "NT_RESTORE_DIRECTORY", "NT_MP4_DIRECTORY", "NT_RUNTIME_PROFILE", "NT_STORAGE_PROFILE"} {
		dir := os.Getenv(k)
		if strings.HasSuffix(k, "PROFILE") {
			dir = filepath.Dir(dir)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return errors.New("production storage is not writable: " + k)
		}
		f, err := os.CreateTemp(dir, ".startup-probe-")
		if err != nil {
			return errors.New("production storage is not writable: " + k)
		}
		name := f.Name()
		err = f.Close()
		removeErr := os.Remove(name)
		if err != nil || removeErr != nil {
			return errors.New("production storage probe failed: " + k)
		}
	}
	return nil
}
