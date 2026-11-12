package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func TestStageAndIndependentRestoreLog(t *testing.T) {
	key, _ := age.GenerateX25519Identity()
	data := []byte("test fixture")
	digest := sha256.Sum256(data)
	m := Manifest{Format: "nt-backup", FormatVersion: 1, Product: "nt-self-hosted", BackupID: "stage-test", InstanceID: "fixture", CreatedAt: time.Now().UTC(), AppVersion: "r11", SchemaVersion: 29, PGMajor: 18, DumpToolMajor: 18, Scope: "control-plane", Includes: []string{"database", "keys"}, Excludes: []string{"external-files"}}
	for _, p := range []string{"database.dump", "keys/recovery-bundle.json"} {
		m.Entries = append(m.Entries, Entry{p, int64(len(data)), hex.EncodeToString(digest[:])})
	}
	var encrypted bytes.Buffer
	if e := WriteArchive(&encrypted, m, key.Recipient().String(), func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }); e != nil {
		t.Fatal(e)
	}
	parent := t.TempDir()
	_, dir, e := Stage(bytes.NewReader(encrypted.Bytes()), key.String(), 29, parent)
	if e != nil {
		t.Fatal(e)
	}
	content, e := os.ReadFile(filepath.Join(dir, "database.dump"))
	if e != nil || !bytes.Equal(content, data) {
		t.Fatal("staged file mismatch", e)
	}
	info, _ := os.Stat(filepath.Join(dir, "database.dump"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("private file mode")
	}
	before, _ := os.ReadDir(parent)
	if _, _, e = Stage(bytes.NewReader(encrypted.Bytes()[:encrypted.Len()-1]), key.String(), 29, parent); e == nil {
		t.Fatal("accepted corrupt file")
	}
	after, _ := os.ReadDir(parent)
	if len(before) != len(after) {
		t.Fatal("corrupt archive left staged output")
	}
	log, e := NewRestoreLog(parent)
	if e != nil {
		t.Fatal(e)
	}
	if e = log.Append("validated"); e != nil {
		t.Fatal(e)
	}
	if e = log.Append("password=secret"); e == nil {
		t.Fatal("accepted arbitrary log state")
	}
	if e = log.Close(); e != nil {
		t.Fatal(e)
	}
	content, e = os.ReadFile(filepath.Join(parent, log.ID, "state.jsonl"))
	if e != nil || strings.Count(string(content), "\n") != 2 {
		t.Fatal("independent log missing", e)
	}
}
