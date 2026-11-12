package application

import (
	"context"
	"errors"
	"fmt"
	"next-terminal/backend/internal/modules/storage"
	"syscall"
	"testing"
)

func TestSharedStorageACLPolicyAndAudit(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	owner := postID(t, srv, "/api/admin/users", userInput("space-owner", nil, nil), admin)
	member := postID(t, srv, "/api/admin/users", userInput("space-member", nil, nil), admin)
	cookie := loginAs(t, srv, "space-member")
	ctx := context.Background()
	s := storage.New(a.db)
	id, e := s.CreateSpace(ctx, owner, "协作文件", 1024)
	if e != nil {
		t.Fatal(e)
	}
	root, e := s.Ensure(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	path := "/api/storage?space=" + id
	expect(t, call(t, srv.URL, "GET", path, nil, cookie, origin), 403)
	acl := "/api/admin/storages/" + id + "/acl"
	expect(t, call(t, srv.URL, "PUT", acl, map[string]any{"userId": member, "writable": false}, admin, origin), 204)
	expect(t, call(t, srv.URL, "GET", path, nil, cookie, origin), 200)
	c := storage.WithActor(ctx, member, "web", "")
	if _, e = s.Create(c, id, root.ID, "denied", false); !errors.Is(e, syscall.EROFS) {
		t.Fatal("readonly ACL", e)
	}
	expect(t, call(t, srv.URL, "PUT", acl, map[string]any{"userId": member, "writable": true}, admin, origin), 204)
	n, e := s.Create(c, id, root.ID, "allowed.txt", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Write(c, id, n.ID, 0, []byte("hello")); e != nil {
		t.Fatal(e)
	}
	before, e := s.Get(c, id, n.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReplaceText(c, id, n.ID, before.Modified, "新的文本"); e != nil {
		t.Fatal(e)
	}
	if e = s.ReplaceText(c, id, n.ID, before.Modified, "stale overwrite"); !errors.Is(e, syscall.ESTALE) {
		t.Fatal("stale editor overwrote file", e)
	}
	if e = s.Rename(c, id, n.ID, root.ID, "renamed.txt"); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", fmt.Sprintf("/api/storage/files/%d/text?space=%s", n.ID, id), nil, cookie, origin), 200)
	if e = s.SetDefault(ctx, member, id); e != nil {
		t.Fatal(e)
	}
	if got, e := s.Default(ctx, member); e != nil || got != id {
		t.Fatal(got, e)
	}
	if e = s.SetPolicy(ctx, member, storage.Policy{Upload: false, Download: false, Copy: false, Paste: false}); e != nil {
		t.Fatal(e)
	}
	if e = s.Write(c, id, n.ID, 0, []byte("x")); !errors.Is(e, syscall.EACCES) {
		t.Fatal("upload policy", e)
	}
	if _, e = s.Read(c, id, n.ID, 0, 5); !errors.Is(e, syscall.EACCES) {
		t.Fatal("download policy", e)
	}
	expect(t, call(t, srv.URL, "GET", fmt.Sprintf("/api/storage/files/%d?space=%s", n.ID, id), nil, cookie, origin), 403)
	if _, e = s.List(c, id, root.ID); e != nil {
		t.Fatal("listing allowed", e)
	}
	expect(t, call(t, srv.URL, "PUT", acl, map[string]any{"userId": member, "revoke": true}, admin, origin), 204)
	if _, e = s.Default(ctx, member); !errors.Is(e, syscall.EACCES) {
		t.Fatal("revoked default", e)
	}
	if _, e = s.Get(c, id, n.ID); !errors.Is(e, syscall.EACCES) {
		t.Fatal("revoked access", e)
	}
	expect(t, call(t, srv.URL, "GET", path, nil, cookie, origin), 403)
	var count int
	if e = a.db.Row(ctx, "SELECT count(*) FROM storage_file_events WHERE actor_id=$1 AND space_id=$2 AND operation IN ('create','write')", member, id).Scan(&count); e != nil || count != 2 {
		t.Fatal("file audit", count, e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/storage/events", nil, cookie, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/admin/storages", nil, cookie, origin), 403)
}
