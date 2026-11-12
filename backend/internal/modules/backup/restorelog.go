package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// RestoreLog stays outside the restored database. Events contain identifiers
// and state only, never SQL output, credentials, or archive payloads.
type RestoreLog struct {
	file *os.File
	ID   string
}
type RestoreEvent struct {
	ID    string    `json:"restoreId"`
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

func NewRestoreLog(parent string) (*RestoreLog, error) {
	dir, e := os.MkdirTemp(parent, "restore-")
	if e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(dir, "state.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	l := &RestoreLog{file: f, ID: filepath.Base(dir)}
	if e = l.Append("preflight"); e != nil {
		f.Close()
		return nil, e
	}
	return l, nil
}
func (l *RestoreLog) Append(state string) error {
	switch state {
	case "preflight", "validated", "safety_backup", "staging", "verifying", "prepared", "switching", "completed", "failed", "rolled_back", "writes_opened":
	default:
		return errors.New("invalid restore state")
	}
	if e := json.NewEncoder(l.file).Encode(RestoreEvent{l.ID, state, time.Now().UTC()}); e != nil {
		return e
	}
	return l.file.Sync()
}
func (l *RestoreLog) Close() error { return l.file.Close() }
func OpenRestoreLog(parent, id string) (*RestoreLog, error) {
	if filepath.Base(id) != id || len(id) < 9 || len(id) > 80 || id[:8] != "restore-" {
		return nil, errors.New("invalid restore identifier")
	}
	// Callers hold the restore workspace lock. A crash can leave only the last
	// append incomplete; discard that uncommitted tail before appending again.
	if _, e := ReadRestoreEvents(parent, id); e != nil {
		return nil, e
	}
	path := filepath.Join(parent, id, "state.jsonl")
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		n := bytes.LastIndexByte(raw, '\n') + 1
		if e = os.Truncate(path, int64(n)); e != nil {
			return nil, e
		}
	}
	f, e := os.OpenFile(filepath.Join(parent, id, "state.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	return &RestoreLog{file: f, ID: id}, nil
}
