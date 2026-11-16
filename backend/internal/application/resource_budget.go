package application

import (
	"context"
	"errors"
	"puppeteer/backend/internal/platform/resourcebudget"
)

// The host journals rollback before draining. These slots stay reserved until
// the host recreates this process, including on recovery after executor failure.
func (a *App) drainHeavyForDeployment() error {
	if a.heavyTasks == nil {
		return errors.New("重任务调度器不可用")
	}
	if !a.resourceDrained.CompareAndSwap(false, true) {
		return nil
	}
	if !a.heavyTasks.TryAcquire(int64(resourcebudget.HeavyLimit(a.config.ResourceProfile))) {
		a.resourceDrained.Store(false)
		return errors.New("后台重任务正在执行或准备启动，请稍后切换资源档位")
	}
	return nil
}

func (a *App) resumeHeavyAfterDeployment() {
	if a.resourceDrained.CompareAndSwap(true, false) {
		a.heavyTasks.Release(int64(resourcebudget.HeavyLimit(a.config.ResourceProfile)))
	}
}

// Acquire before claiming: waiting never consumes a job lease or retry count.
// The semaphore belongs to the single enrolled application's worker group.
func (a *App) acquireHeavy(ctx context.Context) (func(), error) {
	if a.heavyTasks == nil {
		return func() {}, nil
	}
	if err := a.heavyTasks.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { a.heavyTasks.Release(1) }, nil
}

// Retention yields to queued exports/backups, preserving files when busy.
func (a *App) tryHeavy() (func(), bool) {
	if a.heavyTasks == nil {
		return func() {}, true
	}
	if !a.heavyTasks.TryAcquire(1) {
		return nil, false
	}
	return func() { a.heavyTasks.Release(1) }, true
}
