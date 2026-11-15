//go:build linux

package rdpfs

import (
	"context"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"syscall"
	"testing"
)

func TestDeploymentProbeNeverExposesUserDataOrUnboundedWrites(t *testing.T) {
	root := &Root{EnableProbe: true}
	fs.NewNodeFS(root, &fs.Options{})
	var out fuse.EntryOut
	user := fuse.NewContext(context.Background(), &fuse.Caller{Owner: fuse.Owner{Uid: 1000}})
	if _, err := root.Lookup(user, ".deployment-probe", &out); err != syscall.EACCES {
		t.Fatal("guacd accessed diagnostics", err)
	}
	admin := fuse.NewContext(context.Background(), &fuse.Caller{})
	inode, err := root.Lookup(admin, ".deployment-probe", &out)
	if err != 0 {
		t.Fatal(err)
	}
	node := inode.Operations().(*diagnosticFile)
	if _, err = node.Write(user, nil, []byte("x"), 0); err != syscall.EACCES {
		t.Fatal("cached inode bypass")
	}
	if _, err = node.Write(admin, nil, make([]byte, 65), 0); err != syscall.EFBIG {
		t.Fatal("unbounded probe")
	}
	if _, err = node.Write(admin, nil, []byte("probe"), 0); err != 0 {
		t.Fatal(err)
	}
	if _, err = node.Read(user, nil, make([]byte, 5), 0); err != syscall.EACCES {
		t.Fatal("cached inode read")
	}
}
