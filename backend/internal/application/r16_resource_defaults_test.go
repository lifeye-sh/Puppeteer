package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/storage"
	"syscall"
	"testing"
	"time"
)

func TestR16ResourceDefaultsActualConsumers(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	store := storage.New(a.db)
	old := postID(t, srv, "/api/admin/users", userInput("defaults-old", nil, nil), admin)
	fresh := postID(t, srv, "/api/admin/users", userInput("defaults-new", nil, nil), admin)
	if _, e := store.Ensure(ctx, old); e != nil {
		t.Fatal(e)
	}
	if u, e := store.Usage(ctx, old); e != nil || u.Quota != 5<<30 {
		t.Fatal(u, e)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	for _, values := range []map[string]any{
		{"user-default-storage-size": -1, "user-client-cert-valid-days": 2},
		{"user-default-storage-size": 1<<40 + 1},
		{"user-client-cert-valid-days": 0},
		{"user-client-cert-valid-days": 826},
		{"user-client-cert-valid-days": 1.5},
	} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": values}, admin)
		expect(t, r, 422)
	}
	save := func(values map[string]any) {
		t.Helper()
		r, next := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": values}, admin)
		expect(t, r, 202)
		tag = next
	}
	save(map[string]any{"user-default-storage-size": 0, "user-client-cert-valid-days": 2})
	root, e := store.Ensure(ctx, fresh)
	if e != nil {
		t.Fatal(e)
	}
	if u, e := store.Usage(ctx, fresh); e != nil || u.Quota != 0 {
		t.Fatal(u, e)
	}
	n, e := store.Create(ctx, fresh, root.ID, "zero-capacity", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Write(ctx, fresh, n.ID, 0, []byte("x")); !errors.Is(e, syscall.EDQUOT) {
		t.Fatal("zero quota not enforced", e)
	}
	if _, e = store.Ensure(ctx, old); e != nil {
		t.Fatal(e)
	}
	if u, e := store.Usage(ctx, old); e != nil || u.Quota != 5<<30 {
		t.Fatal("existing quota overwritten", u, e)
	}
	member := loginAs(t, srv, "defaults-new")
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]any{"user-default-storage-size": 100}}, member)
	expect(t, r, 403)
	ca := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "defaults CA", "kind": "client_ca", "source": "self_signed", "days": 30}, admin)
	issue := func(days int) string {
		t.Helper()
		return postID(t, srv, "/api/admin/certificates", map[string]any{"name": "defaults client", "kind": "client", "parentId": ca, "userId": fresh, "days": days}, admin)
	}
	original := issue(0)
	cert, e := a.certificates().Get(ctx, original)
	if e != nil {
		t.Fatal(e)
	}
	if d := time.Until(cert.NotAfter); d < 47*time.Hour || d > 49*time.Hour {
		t.Fatal("configured lifetime not applied", d)
	}
	save(map[string]any{"user-default-storage-size": 12345, "user-client-cert-valid-days": 3})
	replacement := postID(t, srv, "/api/admin/certificates/"+original+"/reissue", map[string]any{"version": 1, "days": 0}, admin)
	next, e := a.certificates().Get(ctx, replacement)
	if e != nil {
		t.Fatal(e)
	}
	if d := time.Until(next.NotAfter); d < 71*time.Hour || d > 73*time.Hour {
		t.Fatal("reissue ignored latest default", d)
	}
	previous, e := a.certificates().Get(ctx, original)
	if e != nil || previous.RevokedAt == nil || !previous.NotAfter.Equal(cert.NotAfter) {
		t.Fatal("reissue changed historical certificate", e)
	}
	explicit, e := a.certificates().Get(ctx, issue(1))
	if e != nil {
		t.Fatal(e)
	}
	if d := time.Until(explicit.NotAfter); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatal("explicit lifetime ignored", d)
	}
	save(map[string]any{"user-client-cert-valid-days": 825})
	capped, e := a.certificates().Get(ctx, issue(0))
	if e != nil {
		t.Fatal(e)
	}
	parent, e := a.certificates().Get(ctx, ca)
	if e != nil || !capped.NotAfter.Equal(parent.NotAfter) {
		t.Fatal("issuer expiry exceeded", e)
	}
	newest := postID(t, srv, "/api/admin/users", userInput("defaults-latest", nil, nil), admin)
	if _, e = store.Ensure(ctx, newest); e != nil {
		t.Fatal(e)
	}
	if u, e := store.Usage(ctx, newest); e != nil || u.Quota != 12345 {
		t.Fatal("latest quota ignored", u, e)
	}
	if u, e := store.Usage(ctx, fresh); e != nil || u.Quota != 0 {
		t.Fatal("zero quota reset", u, e)
	}
}
