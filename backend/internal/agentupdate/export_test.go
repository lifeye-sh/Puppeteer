package agentupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublicExportAllowlistAndIntegrity(t *testing.T) {
	dir := t.TempDir()
	repo, keys, binary, out := filepath.Join(dir, "repo"), filepath.Join(dir, "keys"), filepath.Join(dir, "agent"), filepath.Join(dir, "public")
	if e := InitRepository(repo, keys); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(binary, []byte("fixture agent"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := Publish(repo, keys, binary, Release{Sequence: 1, Version: "R17", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(repo, "accidental-private.key"), []byte("never publish"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := ExportRepository(repo, out); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(out, "accidental-private.key")); !os.IsNotExist(e) {
		t.Fatal("non-public file exported")
	}
	if e := ExportRepository(repo, out); e == nil {
		t.Fatal("existing export overwritten")
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(out)))
	defer srv.Close()
	cfg := Config{Repository: srv.URL, RootFile: filepath.Join(repo, "1.root.json"), StateDir: filepath.Join(dir, "state"), AllowHTTPDevelopment: true}
	candidate, e := Check(context.Background(), cfg)
	if e != nil || candidate.Release.Version != "R17" {
		t.Fatalf("export not accepted by real verifier: %v", e)
	}
	if e = os.WriteFile(filepath.Join(repo, "targets", "nt-agent-linux-amd64"), []byte("changed target"), 0600); e != nil {
		t.Fatal(e)
	}
	if ExportRepository(repo, filepath.Join(dir, "bad")) == nil {
		t.Fatal("tampered target exported")
	}
	if _, e = os.Stat(filepath.Join(dir, "bad")); !os.IsNotExist(e) {
		t.Fatal("invalid export left output")
	}
}

func TestSigningDirectoriesMustBeSeparate(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if InitRepository(repo, filepath.Join(repo, "keys")) == nil {
		t.Fatal("private signing keys allowed inside public repo")
	}
	if InitRepository(filepath.Join(dir, "keys", "repo"), filepath.Join(dir, "keys")) == nil {
		t.Fatal("public repo allowed in private signing directory")
	}
	if e := os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(dir, "alias")
	if e := os.Symlink(repo, alias); e != nil {
		t.Skip("symlinks unavailable")
	}
	if InitRepository(repo, filepath.Join(alias, "keys")) == nil {
		t.Fatal("symlink nesting bypassed isolation")
	}
}
