//go:build linux

package rdpfs

import (
	"context"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"syscall"
)

// A bounded, volatile diagnostic inode. It is invisible to guacd (UID 1000),
// never accesses a user's database objects, and exists only for root deployment verification.
type diagnosticFile struct{ fs.MemRegularFile }

func rootCaller(ctx context.Context) bool { c, ok := fuse.FromContext(ctx); return ok && c.Uid == 0 }
func (r *Root) probe(ctx context.Context, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !rootCaller(ctx) {
		return nil, syscall.EACCES
	}
	out.Mode = syscall.S_IFREG | 0600
	n := &diagnosticFile{MemRegularFile: fs.MemRegularFile{Attr: fuse.Attr{Mode: syscall.S_IFREG | 0600}}}
	return r.NewInode(ctx, n, fs.StableAttr{Mode: syscall.S_IFREG}), 0
}
func (n *diagnosticFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if !rootCaller(ctx) {
		return nil, 0, syscall.EACCES
	}
	return nil, fuse.FOPEN_DIRECT_IO, 0
}
func (n *diagnosticFile) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if !rootCaller(ctx) {
		return nil, syscall.EACCES
	}
	return n.MemRegularFile.Read(ctx, f, dest, off)
}
func (n *diagnosticFile) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	if !rootCaller(ctx) {
		return 0, syscall.EACCES
	}
	if off < 0 || off > 64 || int64(len(data)) > 64-off {
		return 0, syscall.EFBIG
	}
	return n.MemRegularFile.Write(ctx, f, data, off)
}
func (n *diagnosticFile) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if !rootCaller(ctx) {
		return syscall.EACCES
	}
	if size, ok := in.GetSize(); ok && size > 64 {
		return syscall.EFBIG
	}
	return n.MemRegularFile.Setattr(ctx, f, in, out)
}
