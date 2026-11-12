package application

import (
	"context"
	"errors"
	"sync"
	"time"
)

func (a *App) enterRequest() bool {
	if a.maintenance.Load() {
		return false
	}
	a.requests.RLock()
	if a.maintenance.Load() {
		a.requests.RUnlock()
		return false
	}
	return true
}

// Background workers are supervised so maintenance waits for their actual exit,
// including task child workers, before taking a snapshot or switching databases.
func (a *App) StartBackgroundServices(ctx context.Context) {
	a.backgroundMu.Lock()
	defer a.backgroundMu.Unlock()
	a.backgroundParent = ctx
	a.startBackgroundLocked()
}
func (a *App) startBackgroundLocked() {
	if a.backgroundParent == nil || a.backgroundParent.Err() != nil {
		return
	}
	if a.backgroundDone != nil {
		select {
		case <-a.backgroundDone:
		default:
			return
		}
	}
	c, cancel := context.WithCancel(a.backgroundParent)
	done := make(chan struct{})
	a.backgroundCancel = cancel
	a.backgroundDone = done
	var wg sync.WaitGroup
	for _, fn := range []func(context.Context){a.RunSessionMaintenance, a.RunStorageMaintenance, a.RunApprovalMaintenance, a.RunCertificateMaintenance, a.RunTaskMaintenance, a.RunAccessRetention, a.RunRecordingExports, a.RunConfiguration, a.RunMonitoring, a.RunDatabaseWorkOrders, a.RunAIJobs} {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(c) }(fn)
	}
	go func() { wg.Wait(); close(done) }()
}
func (a *App) pauseBackground(ctx context.Context) error {
	a.backgroundMu.Lock()
	done := a.backgroundDone
	if a.backgroundCancel != nil {
		a.backgroundCancel()
	}
	a.backgroundMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *App) resumeBackground() {
	a.backgroundMu.Lock()
	done := a.backgroundDone
	a.backgroundMu.Unlock()
	if done != nil {
		<-done
	}
	a.backgroundMu.Lock()
	a.startBackgroundLocked()
	a.backgroundMu.Unlock()
	a.maintenance.Store(false)
}
func (a *App) beginMaintenance(ctx context.Context) (func(), error) {
	if !a.maintenance.CompareAndSwap(false, true) {
		return nil, errors.New("maintenance already active")
	}
	if e := a.pauseBackground(ctx); e != nil {
		go a.resumeBackground()
		return nil, e
	}
	a.StopSessions(ctx)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if a.requests.TryLock() {
			return func() { a.requests.Unlock(); a.resumeBackground() }, nil
		}
		select {
		case <-ctx.Done():
			go a.resumeBackground()
			return nil, errors.New("maintenance drain timed out")
		case <-tick.C:
		}
	}
}
