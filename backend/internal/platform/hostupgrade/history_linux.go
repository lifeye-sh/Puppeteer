//go:build linux

package hostupgrade

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/systemupdate"
	"sort"
	"strings"
	"time"
)

func taskID(name string) bool {
	raw, e := hex.DecodeString(name)
	return e == nil && len(raw) == 16 && strings.ToLower(name) == name
}

func (c *Controller) History() ([]Progress, error) {
	rows, e := c.journals()
	if e != nil {
		return nil, e
	}
	out := []Progress{}
	for _, j := range rows {
		out = append(out, progress(j))
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}
func (c *Controller) journals() ([]systemupdate.UpgradeJournal, error) {
	entries, e := os.ReadDir(c.directory())
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	rows := []systemupdate.UpgradeJournal{}
	seen := map[string]bool{}
	if current, e := systemupdate.OpenTransaction(c.directory(), nil); e == nil {
		j := current.View()
		j.Plan.Envelope = nil
		rows = append(rows, j)
		seen[j.Plan.ID] = true
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !taskID(name) || seen[name] {
			continue
		}
		if j, e := systemupdate.ReadUpgradeTask(c.directory(), name); e == nil {
			j.Plan.Envelope = nil
			rows = append(rows, j)
			seen[name] = true
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Updated.After(rows[j].Updated) })
	return rows, nil
}

func (c *Controller) cleanupAutomationArtifacts(ctx context.Context, now time.Time) {
	c.policyMu.Lock()
	defer c.policyMu.Unlock()
	s, e := c.automationState()
	if e != nil || s.Policy.RetainDays < 30 || now.Sub(s.LastCleanup) < 24*time.Hour {
		return
	}
	e = c.Engine.Maintain(ctx, func(context.Context) error {
		unlock, e := systemupdate.LockTransaction(c.Executor.Directory)
		if e != nil {
			return e
		}
		defer unlock()
		return c.pruneArtifacts(now, s.Policy.RetainDays)
	})
	if errors.Is(e, deploytask.ErrBusy) || errors.Is(e, systemupdate.ErrUpgradeBusy) {
		return
	}
	s.LastCleanup = now
	s.CleanupReason = "已按保留规则检查升级产物，历史记录与镜像保持保留"
	if e != nil {
		s.CleanupReason = "升级产物清理未完成，请检查宿主机目录权限和任务记录"
	}
	_ = c.saveAutomation(s)
}

// Journals and high-water marks are retained forever for idempotence. Only
// successful/prepared job artifacts are eligible; failed recovery is never pruned.
func (c *Controller) pruneArtifacts(now time.Time, days int) error {
	rows, e := c.journals()
	if e != nil {
		return e
	}
	current := ""
	if tx, e := systemupdate.OpenTransaction(c.directory(), nil); e == nil {
		current = tx.View().Plan.ID
	} else if !os.IsNotExist(e) {
		return e
	}
	keptSuccess := 0
	for _, j := range rows {
		if j.Stage != "succeeded" && j.Stage != "prepared" {
			continue
		}
		if j.Stage == "succeeded" {
			keptSuccess++
			if keptSuccess <= 3 {
				continue
			}
		}
		if j.Plan.ID == current || j.Updated.After(now.Add(-time.Duration(days)*24*time.Hour)) {
			continue
		}
		dir := filepath.Join(c.directory(), j.Plan.ID)
		info, e := os.Lstat(dir)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !taskID(j.Plan.ID) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || filepath.Dir(dir) != c.directory() {
			continue
		}
		if e = os.RemoveAll(dir); e != nil {
			return e
		}
	}
	return nil
}
