package backup

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ReadRestoreEvents(parent, id string) ([]RestoreEvent, error) {
	if filepath.Base(id) != id || !strings.HasPrefix(id, "restore-") || len(id) > 80 {
		return nil, errors.New("invalid restore identifier")
	}
	f, e := os.Open(filepath.Join(parent, id, "state.jsonl"))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || info.Size() > 262144 {
		return nil, errors.New("restore log size invalid")
	}
	decoder := json.NewDecoder(f)
	out := []RestoreEvent{}
	for {
		var event RestoreEvent
		e = decoder.Decode(&event)
		if e == io.EOF || e == io.ErrUnexpectedEOF {
			return out, nil
		}
		if e != nil || event.ID != id {
			return nil, errors.New("invalid restore log")
		}
		out = append(out, event)
	}
}

type RestoreHistory struct {
	ID     string         `json:"restoreId"`
	Events []RestoreEvent `json:"events"`
}

func RestoreHistories(parent string) ([]RestoreHistory, error) {
	out := []RestoreHistory{}
	if parent == "" {
		return out, nil
	}
	entries, e := os.ReadDir(parent)
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	type candidate struct {
		name string
		time int64
	}
	dirs := []candidate{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "restore-") {
			info, e := entry.Info()
			if e != nil {
				return nil, e
			}
			dirs = append(dirs, candidate{entry.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].time > dirs[j].time })
	if len(dirs) > 100 {
		dirs = dirs[:100]
	}
	for _, entry := range dirs {
		events, e := ReadRestoreEvents(parent, entry.name)
		if e != nil {
			return nil, e
		}
		out = append(out, RestoreHistory{entry.name, events})
	}
	return out, nil
}
