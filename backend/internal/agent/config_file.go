package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Atomic replacement in the private configuration directory; fsync before acknowledging rotation.
func SaveConfig(path string, c Config) error {
	raw, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".agent-config-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func Rotate(ctx context.Context, path string, c *Config) error {
	if c.PendingKey == "" {
		var b [32]byte
		if _, e := rand.Read(b[:]); e != nil {
			return e
		}
		c.PendingKey = hex.EncodeToString(b[:])
		if e := SaveConfig(path, *c); e != nil {
			return e
		}
	}
	ctx, done := context.WithTimeout(ctx, 15*time.Second)
	defer done()
	raw, _ := json.Marshal(map[string]string{"nextKey": c.PendingKey})
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Endpoint, "/")+"/api/agent/v1/rotate", bytes.NewReader(raw))
	if e != nil {
		return e
	}
	DeviceAuth(req.Header, *c)
	req.Header.Set("Content-Type", "application/json")
	res, e := HTTPClient().Do(req)
	if e != nil {
		return errors.New("rotation unavailable; pending key retained")
	}
	defer res.Body.Close()
	if res.StatusCode != 204 {
		return errors.New("rotation rejected; pending key retained")
	}
	next := *c
	next.DeviceKey = c.PendingKey
	next.PendingKey = ""
	if e = SaveConfig(path, next); e != nil {
		return e
	}
	*c = next
	return nil
}
