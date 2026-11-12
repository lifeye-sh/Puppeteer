package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreLockAndIncompleteAppend(t *testing.T) {
	parent := t.TempDir()
	release, e := AcquireRestoreLock(parent)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if other, e := AcquireRestoreLock(parent); e == nil {
		other()
		t.Fatal("concurrent restore accepted")
	}
	log, e := NewRestoreLog(parent)
	if e != nil {
		t.Fatal(e)
	}
	id := log.ID
	log.Append("validated")
	log.Close()
	f, e := os.OpenFile(filepath.Join(parent, id, "state.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString(`{"restoreId":`)
	f.Close()
	events, e := ReadRestoreEvents(parent, id)
	if e != nil || len(events) != 2 {
		t.Fatal("incomplete log could not be read", e)
	}
	log, e = OpenRestoreLog(parent, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = log.Append("prepared"); e != nil {
		t.Fatal(e)
	}
	log.Close()
	events, e = ReadRestoreEvents(parent, id)
	if e != nil || len(events) != 3 || events[2].State != "prepared" {
		t.Fatal("append repair failed", e)
	}
	if _, e = ReadRestoreEvents(parent, "../restore-other"); e == nil {
		t.Fatal("traversal accepted")
	}
}
