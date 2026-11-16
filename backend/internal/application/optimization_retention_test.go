package application

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"

	"github.com/gorilla/websocket"
)

// Isolate the test helper from application/database state. A closed websocket
// still owns buffers while a parent testing.T cleanup closure references it.
func TestOptimizationCleanupRetention(t *testing.T) {
	if os.Getenv("NT_SOAK_RETENTION") != "1" {
		t.Skip("opt-in heap diagnosis")
	}
	dir := os.Getenv("NT_SOAK_PROFILE_DIR")
	if dir == "" {
		t.Fatal("profile directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err = ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	snapshot := func(name string) uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		f, err := os.Create(filepath.Join(dir, name+".pprof"))
		if err != nil {
			t.Fatal(err)
		}
		if err = pprof.WriteHeapProfile(f); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("RETAIN %s heap=%d objects=%d goroutines=%d", name, m.HeapAlloc, m.HeapObjects, runtime.NumGoroutine())
		return m.HeapAlloc
	}
	cookie := &http.Cookie{Name: "fixture", Value: "test"}
	var legacy, scoped int64
	t.Run("parent_cleanup", func(t *testing.T) {
		before := snapshot("parent-before")
		for i := 0; i < 600; i++ {
			ws := attach(t, srv, cookie, "test", "test")
			ws.Close()
		}
		legacy = int64(snapshot("parent-after")) - int64(before)
	})
	snapshot("parent-released")
	t.Run("cycle_cleanup", func(t *testing.T) {
		before := snapshot("cycle-before")
		for i := 0; i < 600; i++ {
			func() { ws := attachOwned(t, srv, cookie, "test", "test"); defer ws.Close() }()
		}
		scoped = int64(snapshot("cycle-after")) - int64(before)
	})
	if legacy < 2<<20 {
		t.Fatalf("retention reproduction too small: %d", legacy)
	}
	if scoped > legacy/4+(1<<20) {
		t.Fatalf("per-cycle cleanup retained buffers: legacy=%d scoped=%d", legacy, scoped)
	}
	t.Logf("RETAIN comparison parent=%d cycle=%d", legacy, scoped)
}
