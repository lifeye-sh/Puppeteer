// Package hostupgrade supplies fixed host operations for the durable transaction.
package hostupgrade

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/systemupdate"
	"time"
)

//go:embed operations.py
var operations string

// Driver is only constructed from the root-owned enrollment configuration.
// The runner binary embeds its operations, so replacing the application and
// deployment daemon cannot change an in-flight transaction's implementation.
type Driver struct {
	Executor deploytask.ComposeExecutor
	Release  systemupdate.Release
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func (d *Driver) call(ctx context.Context, p systemupdate.UpgradePlan, operation string) error {
	raw, e := json.Marshal(map[string]any{"operation": operation, "executor": d.Executor, "plan": p, "release": d.Release})
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", operations)
	cmd.Stdin = bytes.NewReader(raw)
	out := &cappedBuffer{limit: 65536}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()
	var result struct {
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil {
		return errors.New("宿主机升级操作中断或响应无效")
	}
	if !result.Passed || runErr != nil {
		if result.Reason != "" {
			return &systemupdate.OperationError{Reason: result.Reason}
		}
		return errors.New("宿主机升级操作失败")
	}
	return nil
}
func (d *Driver) Prepare(c context.Context, p systemupdate.UpgradePlan, r systemupdate.Release) error {
	d.Release = r
	return d.call(c, p, "prepare")
}
func (d *Driver) Quiesce(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "quiesce")
}
func (d *Driver) Backup(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "backup")
}
func (d *Driver) ValidateBackup(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "validate")
}
func (d *Driver) Switch(c context.Context, p systemupdate.UpgradePlan, r systemupdate.Release) error {
	d.Release = r
	return d.call(c, p, "switch")
}
func (d *Driver) Verify(c context.Context, p systemupdate.UpgradePlan, r systemupdate.Release) error {
	d.Release = r
	return d.call(c, p, "verify")
}
func (d *Driver) Activate(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "activate")
}
func (d *Driver) Fence(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "fence")
}
func (d *Driver) Restore(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "restore")
}
func (d *Driver) ResumePrevious(c context.Context, p systemupdate.UpgradePlan) error {
	return d.call(c, p, "resume")
}

var _ systemupdate.UpgradeDriver = (*Driver)(nil)
