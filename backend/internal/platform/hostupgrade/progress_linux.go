//go:build linux

package hostupgrade

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"puppeteer/backend/internal/platform/systemupdate"
)

func progressHandler(directory string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		tx, e := systemupdate.OpenTransaction(directory, nil)
		if e != nil || r.Method != "GET" {
			http.Error(w, "progress unavailable", 401)
			return
		}
		j := tx.View()
		token, e := os.ReadFile(filepath.Join(directory, j.Plan.ID, "progress-token"))
		expected := sha256.Sum256(token)
		supplied := sha256.Sum256([]byte(r.URL.Query().Get("ticket")))
		if e != nil || len(token) != 64 || r.URL.Path != "/upgrade-progress/"+j.Plan.ID || time.Since(j.Started) > 4*time.Hour || subtle.ConstantTimeCompare(expected[:], supplied[:]) != 1 {
			http.Error(w, "progress ticket invalid or expired", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(progress(j))
	})
}

// Progress has its own socket and lifecycle; deployd can be replaced while this
// endpoint keeps reading the durable journal. Ticket holders have read-only access.
func ServeProgress(directory, socket string, group int) (func(), error) {
	if !filepath.IsAbs(socket) || group < 0 {
		return nil, errors.New("invalid upgrade progress socket")
	}
	if info, e := os.Lstat(socket); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("refusing to replace non-socket progress path")
		}
		if e = os.Remove(socket); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	listener, e := net.Listen("unix", socket)
	if e != nil {
		return nil, e
	}
	if e = os.Chown(socket, 0, group); e == nil {
		e = os.Chmod(socket, 0660)
	}
	if e != nil {
		listener.Close()
		return nil, e
	}
	server := &http.Server{Handler: progressHandler(directory), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		_ = os.Remove(socket)
	}, nil
}
