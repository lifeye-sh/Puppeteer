package deploytask

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

func (e ComposeExecutor) CheckWALApplication(ctx context.Context) error {
	_, err := e.run(ctx, 15*time.Second, "check WAL runtime", "exec", "-T", "server", "/app/nt", "deployment-wal-guard")
	if err != nil {
		return errors.New("应用未就绪、正在恢复隔离或已切换数据库；WAL 自动任务暂停")
	}
	return nil
}

func (d *HostDriver) checkWALGuard(ctx context.Context) error {
	if d.WALGuard != nil {
		return d.WALGuard(ctx)
	}
	return d.Executor.CheckWALApplication(ctx)
}

// Only disposable resources with both our exact project label and generated
// name are eligible. Source database/archive volumes never match this scope.
func (d *HostDriver) RecoverWALDrills(ctx context.Context) error {
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(d.Executor.Project) + "-wal-drill-[0-9a-f]{16}$")
	label := "label=io.puppeteer.wal-drill=" + d.Executor.Project
	containers, err := d.Executor.execute(ctx, 15*time.Second, "list interrupted restore containers", "ps", "-a", "--filter", label, "--format", "{{.Names}}")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(containers)) {
		if !pattern.MatchString(name) {
			return errors.New("unexpected restore container identity; cleanup refused")
		}
		if _, err = d.Executor.execute(ctx, 15*time.Second, "remove interrupted restore container", "rm", "-f", name); err != nil {
			return err
		}
	}
	volumes, err := d.Executor.execute(ctx, 15*time.Second, "list interrupted restore volumes", "volume", "ls", "--filter", label, "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(volumes)) {
		if !strings.HasSuffix(name, "-data") || !pattern.MatchString(strings.TrimSuffix(name, "-data")) {
			return errors.New("unexpected restore volume identity; cleanup refused")
		}
		if _, err = d.Executor.execute(ctx, 15*time.Second, "remove interrupted restore volume", "volume", "rm", name); err != nil {
			return err
		}
	}
	return nil
}
