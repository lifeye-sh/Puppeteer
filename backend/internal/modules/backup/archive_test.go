package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"filippo.io/age"
	"io"
	"testing"
	"time"
)

func TestArchiveRoundTripAndRejections(t *testing.T) {
	key, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	files := map[string][]byte{"database.dump": []byte("PGDMP fixture"), "keys/recovery-bundle.json": []byte(`{"testOnly":true}`)}
	m := Manifest{Format: "nt-backup", FormatVersion: 1, Product: "nt-self-hosted", BackupID: "backup-test", InstanceID: "instance-test", CreatedAt: time.Now().UTC(), AppVersion: "r11", SchemaVersion: 29, PGMajor: 18, DumpToolMajor: 18, Scope: "control-plane", Includes: []string{"database", "keys"}, Excludes: []string{"external-objects"}}
	for _, p := range []string{"database.dump", "keys/recovery-bundle.json"} {
		h := sha256.Sum256(files[p])
		m.Entries = append(m.Entries, Entry{p, int64(len(files[p])), hex.EncodeToString(h[:])})
	}
	open := func(p string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(files[p])), nil }
	var out bytes.Buffer
	if e := WriteArchive(&out, m, key.Recipient().String(), open); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(out.Bytes(), files["database.dump"]) {
		t.Fatal("plaintext leak")
	}
	got, e := InspectArchive(bytes.NewReader(out.Bytes()), key.String(), 29)
	if e != nil || got.BackupID != m.BackupID {
		t.Fatalf("round trip: %v", e)
	}
	for _, tc := range []struct {
		name     string
		data     []byte
		identity string
		schema   int
	}{
		{"wrong key", out.Bytes(), other.String(), 29},
		{"truncated final authentication", out.Bytes()[:out.Len()-1], key.String(), 29},
		{"future schema", out.Bytes(), key.String(), 28},
		{"trailing ciphertext", append(append([]byte{}, out.Bytes()...), 1), key.String(), 29},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := InspectArchive(bytes.NewReader(tc.data), tc.identity, tc.schema); e == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
	files["database.dump"][0] = 'X'
	if e := WriteArchive(io.Discard, m, key.Recipient().String(), open); e == nil {
		t.Fatal("accepted changed source")
	}
	for _, p := range []string{"../database.dump", "/database.dump", "keys\\recovery-bundle.json", "database.dump"} {
		bad := m
		bad.Entries = append([]Entry{}, m.Entries...)
		bad.Entries[1].Path = p
		if e := bad.Validate(29); e == nil {
			t.Fatalf("accepted invalid path %s", p)
		}
	}
}
