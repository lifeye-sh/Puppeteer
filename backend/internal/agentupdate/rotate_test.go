package agentupdate

import (
	"context"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRootRotationExistingClient(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	keys := filepath.Join(dir, "keys")
	next := filepath.Join(dir, "next")
	nk := filepath.Join(dir, "nextkeys")
	if e := InitRepository(repo, keys); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "binary")
	os.WriteFile(binary, []byte("agent"), 0700)
	if e := Publish(repo, keys, binary, Release{Sequence: 1, Version: "one", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	current := repo
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.FileServer(http.Dir(current)).ServeHTTP(w, r) }))
	defer srv.Close()
	cfg := Config{Repository: srv.URL, RootFile: filepath.Join(repo, "1.root.json"), StateDir: filepath.Join(dir, "state"), AllowHTTPDevelopment: true}
	if _, e := Check(context.Background(), cfg); e != nil {
		t.Fatal(e)
	}
	if e := RotateRoot(repo, keys, next, nk); e != nil {
		t.Fatal(e)
	}
	if e := Publish(next, nk, binary, Release{Sequence: 2, Version: "two", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	public := filepath.Join(dir, "public")
	if e := ExportRepository(next, public); e != nil {
		t.Fatal(e)
	}
	current = public
	candidate, e := Check(context.Background(), cfg)
	if e != nil || candidate.Release.Sequence != 2 {
		t.Fatal(candidate, e)
	}
	if _, e = os.Stat(filepath.Join(repo, "2.root.json")); !os.IsNotExist(e) {
		t.Fatal("active repository modified")
	}
	if e = RotateRoot(next, keys, filepath.Join(dir, "bad"), filepath.Join(dir, "badkeys")); e == nil {
		t.Fatal("old private key accepted")
	}
	root, e := metadata.Root().FromFile(filepath.Join(next, "2.root.json"))
	if e != nil {
		t.Fatal(e)
	}
	// Removing either signature must invalidate the offline export chain.
	signatures := root.Signatures
	for i := range signatures {
		root.Signatures = signatures[i : i+1]
		if e = root.ToFile(filepath.Join(next, "2.root.json"), true); e != nil {
			t.Fatal(e)
		}
		if e = ExportRepository(next, filepath.Join(dir, "invalid")); e == nil {
			t.Fatal("single signature accepted")
		}
	}
}
