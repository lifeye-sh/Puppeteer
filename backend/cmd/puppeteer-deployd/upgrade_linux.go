//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"puppeteer/backend/internal/platform/hostupgrade"
	"puppeteer/backend/internal/platform/systemupdate"
)

// Invoked by a separate host service, from a per-task copy of this binary.
// No application or deployd lifetime context can cancel the upgrade runner.
func runUpgrade(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("upgrade runner requires host root")
	}
	flags := flag.NewFlagSet("upgrade-run", flag.ContinueOnError)
	path := flags.String("config", "/opt/puppeteer/config/deployd.json", "root-owned enrollment configuration")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected upgrade arguments")
	}
	raw, e := privateRead(*path)
	if e != nil {
		return e
	}
	var cfg config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&cfg); e != nil {
		return errors.New("invalid upgrade enrollment")
	}
	unlock, e := systemupdate.LockTransaction(cfg.Executor.Directory)
	if e != nil {
		return e
	}
	defer unlock()
	tx, e := systemupdate.OpenTransaction(filepath.Join(cfg.Executor.Directory, "config", "upgrade-state"), systemupdate.TrustedKeys())
	if e != nil {
		return e
	}
	if tx.View().Terminal() {
		return nil
	}
	closeProgress, e := hostupgrade.ServeProgress(filepath.Join(cfg.Executor.Directory, "config", "upgrade-state"), filepath.Join(filepath.Dir(cfg.Socket), "upgrade.sock"), cfg.SocketGroup)
	if e != nil {
		return e
	}
	defer closeProgress()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if tx.View().Stage == "recovery_required" {
		<-ctx.Done()
		return nil
	}
	e = tx.Run(ctx, &hostupgrade.Driver{Executor: cfg.Executor})
	// A completed rejection/rollback is terminal, not a service restart loop.
	if tx.View().Stage == "recovery_required" {
		<-ctx.Done()
		return nil
	}
	if tx.View().Terminal() {
		return nil
	}
	return e
}
