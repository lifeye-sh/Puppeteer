//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"golang.org/x/sys/unix"
	"log"
	"net"
	"net/http"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/rdpstorage"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Executor       deploytask.ComposeExecutor `json:"executor"`
	StateDirectory string                     `json:"stateDirectory"`
	Socket         string                     `json:"socket"`
	TokenFile      string                     `json:"tokenFile"`
	SocketGroup    int                        `json:"socketGroup"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "rdp-storage" {
		rdpstorage.Run(os.Args[2:])
		return
	}
	path := flag.String("config", "/opt/puppeteer/config/deployd.json", "root-owned daemon configuration")
	flag.Parse()
	if err := run(*path); err != nil {
		log.Fatal(err)
	}
}
func privateRead(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("configuration must be a root-owned regular file without group or world write access")
	}
	return os.ReadFile(path)
}
func run(path string) error {
	if os.Geteuid() != 0 {
		return errors.New("deployd requires host root privileges")
	}
	raw, err := privateRead(path)
	if err != nil {
		return err
	}
	var cfg config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.StateDirectory) || !filepath.IsAbs(cfg.Socket) || cfg.SocketGroup < 0 {
		return errors.New("invalid daemon paths or socket group")
	}
	if err = os.MkdirAll(cfg.StateDirectory, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(cfg.StateDirectory, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("another deployment daemon is running")
	}
	token, err := privateRead(cfg.TokenFile)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	driver := &deploytask.HostDriver{Executor: cfg.Executor, StateDirectory: cfg.StateDirectory, VerifyRuntime: cfg.Executor.VerifyApplication, ApplyWebsite: cfg.Executor.ApplyWebsite}
	driver.Check = cfg.Executor.CheckApplication
	driver.WALGuard = cfg.Executor.CheckWALApplication
	if err = driver.RecoverWALDrills(ctx); err != nil {
		return err
	}

	engine, err := deploytask.Open(ctx, cfg.StateDirectory, driver)
	if err != nil {
		return err
	}
	driver.Progress = engine.Report
	go driver.RunMaintenance(ctx, engine)
	handler, err := deploytask.Handler(ctx, engine, strings.TrimSpace(string(token)))
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(cfg.Socket); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace a non-socket path")
		}
		if err = os.Remove(cfg.Socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	listener, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(cfg.Socket)
	if err = os.Chown(cfg.Socket, 0, cfg.SocketGroup); err != nil {
		return err
	}
	if err = os.Chmod(cfg.Socket, 0660); err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	select {
	case err = <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	shutdownErr := server.Shutdown(shutdown)
	recovery, finish := context.WithTimeout(context.Background(), 9*time.Minute)
	defer finish()
	if err = engine.Wait(recovery); err != nil {
		return err
	}
	return shutdownErr
}
