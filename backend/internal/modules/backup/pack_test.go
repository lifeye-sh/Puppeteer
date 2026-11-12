package backup

import (
	"filippo.io/age"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPackDirectoryAtomicPublication(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "keys"), 0700)
	os.WriteFile(filepath.Join(dir, "database.dump"), []byte("fixture"), 0600)
	os.WriteFile(filepath.Join(dir, "keys", "recovery-bundle.json"), []byte(`{"fixture":true}`), 0600)
	key, _ := age.GenerateX25519Identity()
	m := Manifest{Format: "nt-backup", FormatVersion: 1, Product: "nt-self-hosted", BackupID: "pack", InstanceID: "test", CreatedAt: time.Now().UTC(), AppVersion: "r11", SchemaVersion: 29, PGMajor: 18, DumpToolMajor: 18, Scope: "control-plane", Includes: []string{"database", "keys"}, Excludes: []string{"objects"}}
	dest := filepath.Join(dir, "fixture.ntbackup.age")
	if _, e := PackDirectory(dir, dest, key.Recipient().String(), m); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(dest)
	if _, e := PackDirectory(dir, dest, key.Recipient().String(), m); e == nil {
		t.Fatal("overwrote published backup")
	}
	after, _ := os.ReadFile(dest)
	if string(before) != string(after) {
		t.Fatal("existing backup changed")
	}
	pending, _ := filepath.Glob(filepath.Join(dir, ".backup-attempt-*"))
	if len(pending) != 0 {
		t.Fatal("temporary files leaked")
	}
	file, e := os.Open(dest)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if _, e = InspectArchive(file, key.String(), 29); e != nil {
		t.Fatal(e)
	}
}
