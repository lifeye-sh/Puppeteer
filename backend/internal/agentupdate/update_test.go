package agentupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifiedReleaseAndRollback(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	keys := filepath.Join(dir, "keys")
	if e := InitRepository(repo, keys); e != nil {
		t.Fatal(e)
	}
	// Publishing later releases never needs the offline root private key.
	if e := os.Remove(filepath.Join(keys, "root.key")); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "agent")
	if e := os.WriteFile(binary, []byte("new release"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := Publish(repo, keys, binary, Release{Sequence: 1, Version: "test", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(repo)))
	defer srv.Close()
	cfg := Config{Repository: srv.URL, RootFile: filepath.Join(repo, "1.root.json"), StateDir: filepath.Join(dir, "state"), AllowHTTPDevelopment: true}
	candidate, e := Check(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(binary, []byte("old release"), 0700); e != nil {
		t.Fatal(e)
	}
	e = Install(context.Background(), cfg, candidate, binary, func(context.Context, string) error { return errors.New("unhealthy") })
	if e == nil {
		t.Fatal("unhealthy release installed")
	}
	raw, _ := os.ReadFile(binary)
	if string(raw) != "old release" {
		t.Fatal("rollback failed")
	}
	if e = os.Remove(filepath.Join(cfg.StateDir, "rejected.json")); e != nil {
		t.Fatal(e)
	}
	if e = Install(context.Background(), cfg, candidate, binary, func(context.Context, string) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = Check(context.Background(), cfg); e == nil {
		t.Fatal("same sequence accepted")
	}
	// A new client must still reject an altered signed target.
	if e = os.WriteFile(filepath.Join(repo, "targets", "nt-agent-linux-amd64"), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	cfg.StateDir = filepath.Join(dir, "tamper-state")
	if _, e = Check(context.Background(), cfg); e == nil {
		t.Fatal("tampered target accepted")
	}
	if e = Publish(repo, keys, binary, Release{Sequence: 2, Version: "expired", OS: "linux", Arch: "amd64"}, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	cfg.StateDir = filepath.Join(dir, "expiry-state")
	if _, e = Check(context.Background(), cfg); e == nil {
		t.Fatal("expired metadata accepted")
	}
}

func TestMetadataSignatureAndRollbackRejected(t *testing.T) {
	dir := t.TempDir()
	repo, keys := filepath.Join(dir, "repo"), filepath.Join(dir, "keys")
	if e := InitRepository(repo, keys); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "agent")
	if e := os.WriteFile(binary, []byte("release"), 0700); e != nil {
		t.Fatal(e)
	}
	publish := func(seq int64) {
		t.Helper()
		if e := Publish(repo, keys, binary, Release{Sequence: seq, Version: "test", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	publish(1)
	old := map[string][]byte{}
	for _, name := range []string{"timestamp.json", "snapshot.json", "targets.json"} {
		raw, e := os.ReadFile(filepath.Join(repo, name))
		if e != nil {
			t.Fatal(e)
		}
		old[name] = raw
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(repo)))
	defer srv.Close()
	cfg := Config{Repository: srv.URL, RootFile: filepath.Join(repo, "1.root.json"), StateDir: filepath.Join(dir, "state"), AllowHTTPDevelopment: true}
	publish(2)
	if _, e := Check(context.Background(), cfg); e != nil {
		t.Fatal(e)
	}
	for name, raw := range old {
		if e := os.WriteFile(filepath.Join(repo, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Check(context.Background(), cfg); e == nil {
		t.Fatal("cached metadata rollback accepted")
	}
	var forged map[string]any
	if e := json.Unmarshal(old["timestamp.json"], &forged); e != nil {
		t.Fatal(e)
	}
	forged["signed"].(map[string]any)["version"] = float64(100)
	raw, e := json.Marshal(forged)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(repo, "timestamp.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	cfg.StateDir = filepath.Join(dir, "fresh-state")
	if _, e = Check(context.Background(), cfg); e == nil {
		t.Fatal("forged signature accepted")
	}
}
