package application

import (
	"bytes"
	"context"
	"errors"
	"next-terminal/backend/internal/modules/storage"
	"syscall"
	"testing"
)

func TestStorageUploadPersistenceQuotaAndPublication(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("upload-owner", nil, nil), admin)
	other := postID(t, srv, "/api/admin/users", userInput("upload-other", nil, nil), admin)
	ctx := storage.WithActor(context.Background(), user, "web", "")
	s := storage.New(a.db)
	root, e := s.Ensure(ctx, user)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetQuota(ctx, user, 70000); e != nil {
		t.Fatal(e)
	}
	dir, e := s.Create(ctx, user, root.ID, "folder", true)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.StartUpload(ctx, user, dir.ID, "file.bin", 65539)
	if e != nil {
		t.Fatal(e)
	}
	if items, e := s.List(ctx, user, dir.ID); e != nil || len(items) != 0 {
		t.Fatal("partial file exposed", items, e)
	}
	if _, e = s.StartUpload(ctx, user, dir.ID, "quota.bin", 65539); !errors.Is(e, syscall.EDQUOT) {
		t.Fatal("quota reservation bypass", e)
	}
	if e = s.Remove(ctx, user, dir.ID); !errors.Is(e, syscall.ENOTEMPTY) {
		t.Fatal("pending upload parent removed", e)
	}
	data := bytes.Repeat([]byte{7}, 65536)
	if e = s.UploadPart(ctx, task.ID, 0, data); e != nil {
		t.Fatal(e)
	}
	if _, e = s.FinishUpload(ctx, task.ID); !errors.Is(e, syscall.ESTALE) {
		t.Fatal("incomplete published", e)
	}
	s = storage.New(a.db) // A new service reads all progress from PostgreSQL.
	if current, e := s.Upload(ctx, task.ID); e != nil || current.Received != 65536 {
		t.Fatal("lost persisted progress", current, e)
	}
	if e = s.UploadPart(ctx, task.ID, 0, data); e != nil {
		t.Fatal("idempotent chunk", e)
	}
	if e = s.UploadPart(ctx, task.ID, 0, bytes.Repeat([]byte{8}, 65536)); !errors.Is(e, syscall.ESTALE) {
		t.Fatal("conflicting retry accepted", e)
	}
	if _, e = s.Upload(storage.WithActor(context.Background(), other, "web", ""), task.ID); !errors.Is(e, syscall.ENOENT) {
		t.Fatal("cross-user upload access", e)
	}
	if e = s.UploadPart(ctx, task.ID, 65536, []byte("end")); e != nil {
		t.Fatal(e)
	}
	done, e := s.FinishUpload(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.FinishUpload(ctx, task.ID)
	if e != nil || again.Node != done.Node {
		t.Fatal("non-idempotent commit", e)
	}
	if e = s.CancelUpload(ctx, task.ID); !errors.Is(e, syscall.ESTALE) {
		t.Fatal("completed upload falsely cancelled", e)
	}
	got, e := s.Read(ctx, user, done.Node, 0, 70000)
	if e != nil || !bytes.Equal(got, append(data, []byte("end")...)) {
		t.Fatal("published content mismatch", e)
	}
	usage, _ := s.Usage(ctx, user)
	if usage.Used != 65539 {
		t.Fatal("double quota charge", usage)
	}
	if e = s.Remove(ctx, user, done.Node); e != nil {
		t.Fatal(e)
	}
	cancelled, e := s.StartUpload(ctx, user, dir.ID, "cancel.bin", 65539)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UploadPart(ctx, cancelled.ID, 0, data); e != nil {
		t.Fatal(e)
	}
	if e = s.CancelUpload(ctx, cancelled.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.CancelUpload(ctx, cancelled.ID); e != nil {
		t.Fatal("cancel retry", e)
	}
	expired, e := s.StartUpload(ctx, user, dir.ID, "expire.bin", 65539)
	if e != nil {
		t.Fatal(e)
	}
	a.db.Exec(ctx, "UPDATE storage_upload_tasks SET expires_at=now()-interval '1 second' WHERE id=$1", expired.ID)
	if e = s.ExpireUploads(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.ExpireUploads(ctx); e != nil {
		t.Fatal(e)
	}
	usage, _ = s.Usage(ctx, user)
	if usage.Used != 0 {
		t.Fatal("reservation not released", usage)
	}
	if e = s.Remove(ctx, user, dir.ID); e != nil {
		t.Fatal("completed history blocked directory deletion", e)
	}
}
