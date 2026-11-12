//go:build linux

package application

import (
	"context"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/platform/rdpfs"
	"strings"
	"syscall"
	"testing"
)

func TestRDPDriveSessionACLAndQuota(t *testing.T) {
	_, a, srv := r02Server(t, false)
	a.config.GuacdAddr = "127.0.0.1:1"
	a.config.GuacdRelayHost = "server"
	admin := loginAs(t, srv, "accept-admin")
	cred := postID(t, srv, "/api/admin/credentials", credInput("drive"), admin)
	body := assetInput("RDP drive", cred, "")
	body["protocol"] = "rdp"
	body["certFingerprint"] = "sha256:" + strings.Repeat("ab", 32)
	asset := postID(t, srv, "/api/admin/assets", body, admin)
	ctx := fuse.NewContext(context.Background(), &fuse.Caller{Owner: fuse.Owner{Uid: 1000, Gid: 1000}})
	store := storage.New(a.db)
	root := &rdpfs.Root{DB: a.db, Store: store}
	fs.NewNodeFS(root, &fs.Options{})
	id, _ := reserve(t, srv, admin, asset, false)
	var out fuse.EntryOut
	if _, e := root.Lookup(ctx, id, &out); e != syscall.EACCES {
		t.Fatal("reserved session exposed", e)
	}
	a.db.Exec(ctx, "UPDATE sessions SET state='connecting' WHERE id=$1", id)
	inode, e := root.Lookup(ctx, id, &out)
	if e != 0 {
		t.Fatal("root lookup", e)
	}
	node := inode.Operations().(*rdpfs.Node)
	if e := store.SetQuota(ctx, node.User, 10); e != nil {
		t.Fatal(e)
	}
	file, _, _, e := node.Create(ctx, "中文.txt", syscall.O_RDWR, 0600, &out)
	if e != 0 {
		t.Fatal(e)
	}
	f := file.Operations().(*rdpfs.Node)
	if _, e = f.Write(ctx, nil, []byte("1234567890"), 0); e != 0 {
		t.Fatal(e)
	}
	if _, e = f.Write(ctx, nil, []byte("x"), 10); e != syscall.EDQUOT {
		t.Fatal("quota bypass", e)
	}
	b, err := store.Read(ctx, node.User, f.ID, 0, 10)
	if err != nil || string(b) != "1234567890" {
		t.Fatal("web/FUSE mismatch", err)
	}
	if _, e = node.Lookup(ctx, "../other", &out); e == 0 {
		t.Fatal("traversal")
	}
	untrusted := fuse.NewContext(context.Background(), &fuse.Caller{Owner: fuse.Owner{Uid: 1234}})
	if _, _, e = f.Open(untrusted, syscall.O_RDONLY); e != syscall.EACCES {
		t.Fatal("OS ACL bypass")
	}
	other := rdpfs.Node{Root: f.Root, Session: f.Session, ID: f.ID, User: "00000000-0000-0000-0000-000000000000"}
	if _, e = other.Read(ctx, nil, make([]byte, 10), 0); e != syscall.EACCES {
		t.Fatal("owner bypass")
	}
	a.db.Exec(ctx, "UPDATE sessions SET readonly=true WHERE id=$1", id)
	if _, e = f.Write(ctx, nil, []byte("x"), 0); e != syscall.EROFS {
		t.Fatal("readonly write")
	}
	if e = node.Unlink(ctx, "中文.txt"); e != syscall.EROFS {
		t.Fatal("readonly delete")
	}
	if _, e = f.Read(ctx, nil, make([]byte, 10), 0); e != 0 {
		t.Fatal("readonly read", e)
	}
	if err := store.SetPolicy(ctx, node.User, storage.Policy{Upload: true, Download: false, Copy: true, Paste: true}); err != nil {
		t.Fatal(err)
	}
	if _, e = f.Read(ctx, nil, make([]byte, 10), 0); e != syscall.EACCES {
		t.Fatal("FUSE download policy bypass", e)
	}
	if err := store.SetPolicy(ctx, node.User, storage.Policy{Upload: true, Download: true, Copy: true, Paste: true}); err != nil {
		t.Fatal(err)
	}
	owner := postID(t, srv, "/api/admin/users", userInput("drive-space-owner", nil, nil), admin)
	space, err := store.CreateSpace(ctx, owner, "共享盘 ACL 测试", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(ctx, "INSERT INTO storage_acl VALUES($1,$2,true)", space, node.User); err != nil {
		t.Fatal(err)
	}
	if err = store.SetDefault(ctx, node.User, space); err != nil {
		t.Fatal(err)
	}
	if _, err = store.BindSession(ctx, node.User, id); err != nil {
		t.Fatal(err)
	}
	a.db.Exec(ctx, "UPDATE sessions SET readonly=false WHERE id=$1", id)
	shared, code := root.Lookup(ctx, id, &out)
	if code != 0 {
		t.Fatal(code)
	}
	sharedNode := shared.Operations().(*rdpfs.Node)
	if sharedNode.Space != space {
		t.Fatal("wrong bound space")
	}
	if _, _, _, code = sharedNode.Create(ctx, "shared.txt", syscall.O_RDWR, 0600, &out); code != 0 {
		t.Fatal(code)
	}
	a.db.Exec(ctx, "DELETE FROM storage_acl WHERE space_id=$1 AND user_id=$2", space, node.User)
	if _, code = sharedNode.Lookup(ctx, "shared.txt", &out); code != syscall.EACCES {
		t.Fatal("FUSE revoked ACL bypass", code)
	}
	a.db.Exec(ctx, "UPDATE sessions SET terminate_requested=true WHERE id=$1", id)
	if _, e = f.Read(ctx, nil, make([]byte, 10), 0); e != syscall.EACCES {
		t.Fatal("revoked session read")
	}
}
