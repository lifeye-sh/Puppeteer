//go:build linux

// Package rdpfs exposes quota-backed storage through session-scoped RDP roots.
// No path supplied by a remote desktop is resolved on the host filesystem.
package rdpfs

import (
	"context"
	"errors"
	"regexp"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/platform/postgres"
)

type Root struct {
	fs.Inode
	DB    *postgres.DB
	Store *storage.Service
}
type Node struct {
	fs.Inode
	Root                 *Root
	Session, User, Space string
	ID                   int64
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func errno(e error) syscall.Errno {
	if e == nil {
		return 0
	}
	var n syscall.Errno
	if errors.As(e, &n) {
		return n
	}
	return syscall.EIO
}
func caller(ctx context.Context) bool {
	c, ok := fuse.FromContext(ctx)
	return ok && (c.Uid == 0 || c.Uid == 1000)
}
func (r *Root) Session(ctx context.Context, id string) (string, bool, error) {
	if !uuid.MatchString(id) {
		return "", false, syscall.EACCES
	}
	var user string
	var ro bool
	e := r.DB.Row(ctx, `SELECT s.actor_id::text,s.readonly FROM sessions s JOIN users u ON u.id=s.actor_id JOIN resource_registry a ON a.id=s.asset_id WHERE s.id=$1 AND s.state IN ('connecting','connected') AND NOT s.terminate_requested AND s.lease_expires_at>now() AND u.status='enabled' AND a.enabled AND a.deleted_at IS NULL AND s.protocol='rdp'`, id).Scan(&user, &ro)
	if e != nil {
		return "", false, syscall.EACCES
	}
	return user, ro, nil
}
func (n *Node) access(ctx context.Context, write bool) syscall.Errno {
	if !caller(ctx) {
		return syscall.EACCES
	}
	u, ro, e := n.Root.Session(ctx, n.Session)
	if e != nil || u != n.User {
		return syscall.EACCES
	}
	var space string
	if e := n.Root.DB.Row(ctx, "SELECT coalesce((SELECT space_id FROM session_storage WHERE session_id=$1),$2::uuid)::text", n.Session, n.User).Scan(&space); e != nil || space != n.Space {
		return syscall.EACCES
	}
	if e := n.Root.Store.Access(ctx, n.User, n.Space, write); e != nil {
		return errno(e)
	}
	if write && ro {
		return syscall.EROFS
	}
	return 0
}
func attrs(v storage.Node, a *fuse.Attr) {
	a.Mode = syscall.S_IFREG | 0600
	if v.Directory {
		a.Mode = syscall.S_IFDIR | 0700
	}
	a.Uid = 1000
	a.Gid = 1000
	a.Size = uint64(v.Size)
	a.Nlink = 1
	a.Mtime = uint64(v.Modified.Unix())
	a.Ctime = a.Mtime
	a.Atime = a.Mtime
	a.Blksize = storage.ChunkSize
	a.Blocks = (a.Size + 511) / 512
}
func (r *Root) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0711
	return 0
}
func (r *Root) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream(nil), 0
}
func (r *Root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !caller(ctx) {
		return nil, syscall.EACCES
	}
	u, _, e := r.Session(ctx, name)
	if e != nil {
		return nil, errno(e)
	}
	var space string
	if e := r.DB.Row(ctx, "SELECT coalesce((SELECT space_id FROM session_storage WHERE session_id=$1),$2::uuid)::text", name, u).Scan(&space); e != nil {
		return nil, syscall.EIO
	}
	v, e := r.Store.Ensure(ctx, space)
	if e != nil {
		return nil, errno(e)
	}
	if e := r.Store.Access(ctx, u, space, false); e != nil {
		return nil, errno(e)
	}
	attrs(v, &out.Attr)
	return r.NewInode(ctx, &Node{Root: r, Session: name, User: u, Space: space, ID: v.ID}, fs.StableAttr{Mode: syscall.S_IFDIR}), 0
}
func (n *Node) child(ctx context.Context, v storage.Node, out *fuse.EntryOut) *fs.Inode {
	attrs(v, &out.Attr)
	return n.NewInode(ctx, &Node{Root: n.Root, Session: n.Session, User: n.User, Space: n.Space, ID: v.ID}, fs.StableAttr{Mode: out.Mode & syscall.S_IFMT})
}
func (n *Node) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if e := n.access(ctx, false); e != 0 {
		return e
	}
	v, e := n.Root.Store.Get(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID)
	if e != nil {
		return errno(e)
	}
	attrs(v, &out.Attr)
	return 0
}
func (n *Node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if e := n.access(ctx, false); e != 0 {
		return nil, e
	}
	v, e := n.Root.Store.Lookup(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, name)
	if e != nil {
		return nil, errno(e)
	}
	return n.child(ctx, v, out), 0
}
func (n *Node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	if e := n.access(ctx, false); e != 0 {
		return nil, e
	}
	vs, e := n.Root.Store.List(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID)
	if e != nil {
		return nil, errno(e)
	}
	out := []fuse.DirEntry{}
	for _, v := range vs {
		mode := uint32(syscall.S_IFREG)
		if v.Directory {
			mode = syscall.S_IFDIR
		}
		out = append(out, fuse.DirEntry{Name: v.Name, Mode: mode})
	}
	return fs.NewListDirStream(out), 0
}
func (n *Node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if e := n.access(ctx, flags&syscall.O_ACCMODE != syscall.O_RDONLY || flags&syscall.O_TRUNC != 0); e != 0 {
		return nil, 0, e
	}
	if flags&syscall.O_TRUNC != 0 {
		if e := n.Root.Store.Resize(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, 0); e != nil {
			return nil, 0, errno(e)
		}
	}
	return nil, fuse.FOPEN_DIRECT_IO, 0
}
func (n *Node) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if e := n.access(ctx, false); e != 0 {
		return nil, e
	}
	b, e := n.Root.Store.Read(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, off, len(dest))
	return fuse.ReadResultData(b), errno(e)
}
func (n *Node) Write(ctx context.Context, f fs.FileHandle, b []byte, off int64) (uint32, syscall.Errno) {
	if e := n.access(ctx, true); e != 0 {
		return 0, e
	}
	if e := n.Root.Store.Write(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, off, b); e != nil {
		return 0, errno(e)
	}
	return uint32(len(b)), 0
}
func (n *Node) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if e := n.access(ctx, true); e != 0 {
		return e
	}
	// Ownership and permissions are fixed by the platform; remote chmod/chown cannot broaden access.
	if uid, ok := in.GetUID(); ok && uid != 1000 {
		return syscall.EPERM
	}
	if gid, ok := in.GetGID(); ok && gid != 1000 {
		return syscall.EPERM
	}
	if size, ok := in.GetSize(); ok {
		if size > 1<<40 {
			return syscall.EFBIG
		}
		if e := n.Root.Store.Resize(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, int64(size)); e != nil {
			return errno(e)
		}
	}
	return n.Getattr(ctx, f, out)
}
func (n *Node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if e := n.access(ctx, true); e != 0 {
		return nil, e
	}
	v, e := n.Root.Store.Create(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, name, true)
	if e != nil {
		return nil, errno(e)
	}
	return n.child(ctx, v, out), 0
}
func (n *Node) Create(ctx context.Context, name string, flags, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if e := n.access(ctx, true); e != 0 {
		return nil, nil, 0, e
	}
	v, e := n.Root.Store.Create(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, name, false)
	if e != nil {
		return nil, nil, 0, errno(e)
	}
	return n.child(ctx, v, out), nil, fuse.FOPEN_DIRECT_IO, 0
}
func (n *Node) Unlink(ctx context.Context, name string) syscall.Errno {
	if e := n.access(ctx, true); e != 0 {
		return e
	}
	v, e := n.Root.Store.Lookup(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, name)
	if e != nil {
		return errno(e)
	}
	return errno(n.Root.Store.Remove(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, v.ID))
}
func (n *Node) Rmdir(ctx context.Context, name string) syscall.Errno { return n.Unlink(ctx, name) }
func (n *Node) Rename(ctx context.Context, name string, parent fs.InodeEmbedder, newname string, flags uint32) syscall.Errno {
	if e := n.access(ctx, true); e != 0 {
		return e
	}
	p, ok := parent.(*Node)
	if !ok || p.User != n.User || p.Space != n.Space || p.Session != n.Session {
		return syscall.EXDEV
	}
	if e := p.access(ctx, true); e != 0 {
		return e
	}
	if flags != 0 {
		return syscall.EINVAL
	}
	v, e := n.Root.Store.Lookup(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, n.ID, name)
	if e != nil {
		return errno(e)
	}
	return errno(n.Root.Store.RenameReplace(storage.WithActor(ctx, n.User, "rdp", n.Session), n.Space, v.ID, p.ID, newname))
}
func (n *Node) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	if e := n.access(ctx, false); e != 0 {
		return e
	}
	u, e := n.Root.Store.Usage(ctx, n.Space)
	if e != nil {
		return errno(e)
	}
	out.Bsize = 4096
	out.Frsize = 4096
	out.Blocks = uint64(u.Quota / 4096)
	out.Bfree = uint64(max(int64(0), u.Quota-u.Used) / 4096)
	out.Bavail = out.Bfree
	out.NameLen = 255
	return 0
}
func (n *Node) Fsync(ctx context.Context, f fs.FileHandle, flags uint32) syscall.Errno {
	return n.access(ctx, false)
}
