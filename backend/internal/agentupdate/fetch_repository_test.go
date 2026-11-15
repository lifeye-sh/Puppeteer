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

func TestFetchRepositoryTrustRollbackAndAllowlist(t *testing.T) {
	dir := t.TempDir()
	repo, keys := filepath.Join(dir, "repo"), filepath.Join(dir, "keys")
	if e := InitRepository(repo, keys); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "agent")
	os.WriteFile(binary, []byte("fixture target"), 0700)
	if e := Publish(repo, keys, binary, Release{Sequence: 2, Version: "fixture", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(repo, "secret.key"), []byte("must not publish"), 0600)
	trust, _ := os.ReadFile(filepath.Join(repo, "1.root.json"))
	vendor := httptest.NewTLSServer(http.FileServer(http.Dir(repo)))
	defer vendor.Close()
	if _, e := FetchRepository(context.Background(), vendor.URL, trust, filepath.Join(dir, "untrusted-tls"), 0); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
	previous := http.DefaultTransport
	http.DefaultTransport = vendor.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	out := filepath.Join(dir, "public")
	release, e := FetchRepository(context.Background(), vendor.URL, trust, out, 1)
	if e != nil || release.Sequence != 2 {
		t.Fatal(release, e)
	}
	if _, e = os.Stat(filepath.Join(out, "secret.key")); !os.IsNotExist(e) {
		t.Fatal("private file exported")
	}
	if _, e = FetchRepository(context.Background(), vendor.URL, trust, filepath.Join(dir, "rollback"), 3); e == nil {
		t.Fatal("rollback accepted")
	}
	if _, e = FetchRepository(context.Background(), vendor.URL, trust, filepath.Join(dir, "root-rollback"), 0, 2); e == nil {
		t.Fatal("root rollback accepted")
	}
	other := filepath.Join(dir, "other")
	if e = InitRepository(other, filepath.Join(dir, "other-keys")); e != nil {
		t.Fatal(e)
	}
	wrong, _ := os.ReadFile(filepath.Join(other, "1.root.json"))
	if _, e = FetchRepository(context.Background(), vendor.URL, wrong, filepath.Join(dir, "wrong-root"), 0); e == nil {
		t.Fatal("untrusted root accepted")
	}
	os.WriteFile(filepath.Join(repo, "targets/nt-agent-linux-amd64"), []byte("tampered target"), 0600)
	if _, e = FetchRepository(context.Background(), vendor.URL, trust, filepath.Join(dir, "tampered"), 0); e == nil {
		t.Fatal("tampered target accepted")
	}
}
