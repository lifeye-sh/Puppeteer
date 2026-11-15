//go:build linux

package rdpstorage

import (
	"context"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"puppeteer/backend/internal/modules/entitlements"
	"puppeteer/backend/internal/modules/storage"
	"puppeteer/backend/internal/platform/deployment"
	"puppeteer/backend/internal/platform/postgres"
	"puppeteer/backend/internal/platform/rdpfs"
	"puppeteer/backend/internal/platform/runtimeprofile"
)

const mountpoint = "/mnt/nt-rdp"

func Run(args []string) {
	if len(args) > 0 && (args[0] == "health" || args[0] == "verify") {
		if err := CheckMount(); err != nil {
			log.Fatal(err)
		}
		if args[0] == "verify" {
			if err := VerifyIO(); err != nil {
				log.Fatal(err)
			}
		}
		c, e := net.DialTimeout("tcp", "127.0.0.1:4822", time.Second)
		if e != nil {
			os.Exit(1)
		}
		c.Close()
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if e := deployment.LoadSecrets(); e != nil {
		log.Fatal("invalid storage file secrets")
	}
	if e := runtimeprofile.ApplyStorage(); e != nil {
		log.Fatal("invalid storage runtime profile")
	}
	cfg, e := pgxpool.ParseConfig(os.Getenv("NT_DATABASE_URL"))
	if e != nil {
		log.Fatal("invalid storage database configuration")
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, e := c.Exec(ctx, `SELECT pg_advisory_lock_shared(110031)`)
		return e
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		log.Fatal("storage database unavailable")
	}
	defer pool.Close()
	if e = pool.Ping(ctx); e != nil {
		log.Fatal("storage database unavailable")
	}
	if e = os.MkdirAll(mountpoint, 0755); e != nil {
		log.Fatal(e)
	}
	db := postgres.New(pool)
	local, verifier, e := entitlements.LoadEnvironment()
	if e != nil {
		log.Fatal("invalid storage entitlement configuration")
	}
	licensing := entitlements.NewService(db, local, verifier)
	db.SetAdmissionCheck(licensing.Admit)
	zero := time.Duration(0)
	srv, e := fs.Mount(mountpoint, &rdpfs.Root{DB: db, Store: storage.New(db), EnableProbe: true}, &fs.Options{MountOptions: fuse.MountOptions{AllowOther: true, DirectMount: true, FsName: "nt-user-storage", Name: "nt-storage", Options: []string{"nosuid", "nodev", "noexec", "default_permissions"}, MaxWrite: 1 << 20}, EntryTimeout: &zero, AttrTimeout: &zero, NegativeTimeout: &zero})
	if e != nil {
		log.Fatal(e)
	}
	defer func() {
		if err := srv.Unmount(); err != nil {
			log.Printf("storage unmount failed")
		}
	}()
	if e = VerifyIO(); e != nil {
		srv.Unmount()
		log.Fatal("storage mounted I/O verification failed")
	}
	cmd := exec.Command("/opt/guacamole/sbin/guacd", "-f", "-b", "0.0.0.0", "-L", "info")
	// Never pass database credentials to guacd or run its protocol workers as root.
	cmd.Env = []string{"PATH=/opt/guacamole/bin:/opt/guacamole/sbin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000}, Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		log.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	case e := <-done:
		log.Printf("guacd stopped: %v", e)
	}
}
