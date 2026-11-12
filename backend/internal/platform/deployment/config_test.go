package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func productionEnv() map[string]string {
	return map[string]string{"NT_ENV": "production", "NT_ALLOWED_ORIGINS": "https://puppeteer.example.com", "NT_MFA_KEY": strings.Repeat("a1", 32), "NT_VAULT_KEY": strings.Repeat("b2", 32), "NT_DATABASE_URL": "postgres://user:secret@postgres/db?sslmode=disable", "NT_DATABASE_NETWORK": "private-container", "NT_BACKUP_DIRECTORY": "/data/backups", "NT_RESTORE_DIRECTORY": "/data/restore", "NT_MP4_DIRECTORY": "/data/mp4", "NT_RUNTIME_PROFILE": "/data/runtime/active.json", "NT_STORAGE_PROFILE": "/data/storage/active.json"}
}
func TestProductionValidation(t *testing.T) {
	env := productionEnv()
	get := func(k string) string { return env[k] }
	if err := Validate(get); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ key, value string }{
		{"NT_ENV", "staging"}, {"NT_ALLOWED_ORIGINS", ""}, {"NT_ALLOWED_ORIGINS", "http://example.com"}, {"NT_ALLOWED_ORIGINS", "https://user:secret@example.com"}, {"NT_ALLOWED_ORIGINS", "https://example.com/path"}, {"NT_ALLOWED_ORIGINS", "https://*.example.com"}, {"NT_MFA_KEY", "bad"}, {"NT_VAULT_KEY", strings.Repeat("a1", 32)}, {"NT_DATABASE_NETWORK", ""}, {"NT_DATABASE_URL", "postgres://user:secret@external/db?sslmode=disable"}, {"NT_MP4_URL", "http://renderer:8090"}, {"NT_BACKUP_DIRECTORY", "relative"},
	} {
		t.Run(item.key+item.value, func(t *testing.T) {
			old := env[item.key]
			env[item.key] = item.value
			defer func() { env[item.key] = old }()
			if Validate(get) == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	env["NT_DATABASE_URL"] = "postgres://user:secret@external/db?sslmode=verify-full"
	if err := Validate(get); err != nil {
		t.Fatal(err)
	}
}
func TestFileSecrets(t *testing.T) {
	for _, k := range []string{"NT_DATABASE_URL", "NT_MFA_KEY", "NT_VAULT_KEY", "NT_MP4_TOKEN"} {
		t.Setenv(k, "")
		t.Setenv(k+"_FILE", "")
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NT_MFA_KEY_FILE", path)
	if err := LoadSecrets(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("NT_MFA_KEY") != "fixture" {
		t.Fatal("secret not loaded")
	}
	if LoadSecrets() == nil {
		t.Fatal("ambiguous secret sources accepted")
	}
	t.Setenv("NT_MFA_KEY", "")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 16385)), 0600); err != nil {
		t.Fatal(err)
	}
	if LoadSecrets() == nil {
		t.Fatal("oversized secret accepted")
	}
}
