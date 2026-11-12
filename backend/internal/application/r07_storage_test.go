package application

import (
	"context"
	"errors"
	"fmt"
	"next-terminal/backend/internal/modules/storage"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestUserStorageQuotaAndIsolation(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("storage-owner", nil, nil), admin)
	other := postID(t, srv, "/api/admin/users", userInput("storage-other", nil, nil), admin)
	s := storage.New(a.db)
	ctx := context.Background()
	root, e := s.Ensure(ctx, user)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Ensure(ctx, other); e != nil {
		t.Fatal(e)
	}
	usage, e := s.Usage(ctx, user)
	if e != nil || usage.Quota != 5<<30 {
		t.Fatal("default quota", usage, e)
	}
	if e = s.SetQuota(ctx, user, 100); e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, e := s.Create(ctx, user, root.ID, fmt.Sprint(i), false)
			if e != nil {
				t.Error(e)
				return
			}
			e = s.Write(ctx, user, n.ID, 0, make([]byte, 60))
			if e == nil {
				successes.Add(1)
			} else if !errors.Is(e, syscall.EDQUOT) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent quota bypass", successes.Load())
	}
	usage, _ = s.Usage(ctx, user)
	if usage.Used != 60 {
		t.Fatal("usage drift", usage)
	}
	n, e := s.Create(ctx, user, root.ID, "sparse", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Write(ctx, user, n.ID, 9, []byte("x")); e != nil {
		t.Fatal(e)
	}
	b, e := s.Read(ctx, user, n.ID, 0, 10)
	if e != nil || len(b) != 10 || b[0] != 0 || b[9] != 'x' {
		t.Fatal("sparse read", b, e)
	}
	if e = s.Resize(ctx, user, n.ID, 5); e != nil {
		t.Fatal(e)
	}
	if e = s.Resize(ctx, user, n.ID, 10); e != nil {
		t.Fatal(e)
	}
	b, _ = s.Read(ctx, user, n.ID, 0, 10)
	if b[9] != 0 {
		t.Fatal("truncated data reappeared")
	}
	if _, e = s.Get(ctx, other, n.ID); !errors.Is(e, syscall.ENOENT) {
		t.Fatal("cross user access", e)
	}
	if e = s.Write(ctx, other, n.ID, 0, []byte("bad")); !errors.Is(e, syscall.ENOENT) {
		t.Fatal("cross user write", e)
	}
	for _, name := range []string{"../escape", "..", "a/b", "a\\b", "a:stream"} {
		if _, e = s.Create(ctx, user, root.ID, name, false); e == nil {
			t.Fatal("unsafe filename", name)
		}
	}
	if e = s.SetQuota(ctx, user, 1); e != nil {
		t.Fatal(e)
	}
	if e = s.Write(ctx, user, n.ID, 10, []byte("x")); !errors.Is(e, syscall.EDQUOT) {
		t.Fatal("reduced quota ignored", e)
	}
	if e = s.Remove(ctx, user, n.ID); e != nil {
		t.Fatal(e)
	}
	cookie := loginAs(t, srv, "storage-owner")
	denied := loginAs(t, srv, "storage-other")
	expect(t, call(t, srv.URL, "GET", "/api/storage", nil, cookie, origin), 200)
	expect(t, call(t, srv.URL, "GET", fmt.Sprintf("/api/storage?parent=%d", root.ID), nil, denied, origin), 404)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+user+"/storage", map[string]any{"quotaBytes": 100}, cookie, origin), 403)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/users/"+user+"/storage", map[string]any{"quotaBytes": 1000}, admin, origin), 204)
}
