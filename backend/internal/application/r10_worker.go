package application

import (
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"log/slog"
	"net"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func (a *App) taskHandlers() jobs.Registry {
	return jobs.Registry{"ssh.command": a.executeTaskJob, "credential.rotate": a.executeRotationJob}
}
func (a *App) RunTaskMaintenance(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if e := a.RunTaskOnce(ctx); e != nil && ctx.Err() == nil {
						slog.Error("task worker failed; lease recovery will prevent replay")
					}
				}
			}
		}()
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c, stop := context.WithTimeout(ctx, 5*time.Second)
			e := a.RecoverTaskJobs(c)
			if e == nil {
				for i := 0; i < 10; i++ {
					if e = automation.New(a.db).Tick(c, jobs.New(a.db)); e != nil {
						break
					}
				}
			}
			stop()
			if e != nil && ctx.Err() == nil {
				slog.Error("task scheduler maintenance failed")
			}
		}
	}
}
func (a *App) RecoverTaskJobs(ctx context.Context) error {
	return a.db.Within(ctx, func(c context.Context) error {
		ids, e := jobs.New(a.db).Recover(c)
		if e != nil {
			return e
		}
		for _, id := range ids {
			if e = a.vault.RecoverRotation(c, id); e != nil {
				return e
			}
			targets, e := a.vault.RotationTargets(c, id)
			if e != nil {
				return e
			}
			for _, t := range targets {
				if e = automation.New(a.db).TargetDone(c, id, automation.Target{AssetID: t.AssetID, State: string(t.State), Reason: t.Reason}); e != nil {
					return e
				}
			}
			if len(targets) > 0 {
				counts := map[string]int{}
				for _, t := range targets {
					counts[string(t.State)]++
				}
				if counts["unknown"] == 0 {
					state := "cancelled"
					if counts["succeeded"] == len(targets) {
						state = "succeeded"
					} else if counts["succeeded"] > 0 {
						state = "partial"
					} else if counts["failed"] > 0 {
						state = "failed"
					}
					if e = jobs.New(a.db).RecoverResult(c, id, state); e != nil {
						return e
					}
				}
			}
			if e = automation.New(a.db).RecoverRun(c, id); e != nil {
				return e
			}
		}
		return nil
	})
}
func (a *App) RunTaskOnce(ctx context.Context) error {
	q := jobs.New(a.db)
	for kind, handler := range a.taskHandlers() {
		claim, stop := context.WithTimeout(ctx, 5*time.Second)
		job, e := q.Claim(claim, kind)
		stop()
		if jobs.IsEmpty(e) {
			continue
		}
		if e != nil {
			return e
		}
		c, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-c.Done():
					return
				case <-tick.C:
					h, stop := context.WithTimeout(c, 2*time.Second)
					e := q.Heartbeat(h, job)
					stop()
					if e != nil {
						cancel()
						return
					}
				}
			}
		}()
		e = handler(c, job)
		cancel()
		<-done
		return e
	}
	return nil
}
func (a *App) executeTaskJob(ctx context.Context, job jobs.Job) error {
	s := automation.New(a.db)
	q := jobs.New(a.db)
	run, e := s.Run(ctx, job.ID)
	if e != nil {
		return e
	}
	counts := map[string]int{}
	for _, asset := range run.Snapshot.AssetIDs {
		target := automation.Target{AssetID: asset, State: "cancelled", Reason: "cancelled_before_dispatch"}
		if ctx.Err() == nil {
			target = a.executeTaskTarget(ctx, job, run, asset)
		}
		counts[target.State]++
		c, stop := context.WithTimeout(context.Background(), 3*time.Second)
		e = a.db.Within(c, func(c context.Context) error {
			if e := q.Owned(c, job); e != nil {
				return e
			}
			return s.TargetDone(c, job.ID, target)
		})
		stop()
		if e != nil {
			return e
		}
	}
	state := "succeeded"
	reason := ""
	switch {
	case counts["unknown"] > 0:
		state = "unknown"
		reason = "remote_result_requires_review"
	case counts["succeeded"] > 0 && (counts["failed"]+counts["cancelled"] > 0):
		state = "partial"
	case counts["failed"] > 0:
		state = "failed"
	case counts["cancelled"] > 0:
		state = "cancelled"
	}
	c, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	return a.db.Within(c, func(c context.Context) error {
		if e := q.Finish(c, job, state, reason); e != nil {
			return e
		}
		if e := s.PauseUnknown(c, job.ID); e != nil {
			return e
		}
		return a.audit.Detail(c, run.ActorID, "task.completed", "task:"+job.ID, run.Snapshot.SourceIP, map[string]string{"id": job.ID, "state": state})
	})
}

// Truncate stored output while continuing to drain SSH streams to avoid deadlocks.
type taskOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (w *taskOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	keep := 65536 - len(w.data)
	if len(p) > keep {
		w.truncated = true
		p = p[:keep]
	}
	w.data = append(w.data, p...)
	return n, nil
}
func (w *taskOutput) snapshot() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	value := strings.ReplaceAll(strings.ToValidUTF8(string(w.data), "�"), "\x00", "�")
	truncated := w.truncated
	if len(value) > 65536 {
		value = value[:65536]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		truncated = true
	}
	return value, truncated
}

func (a *App) executeTaskTarget(parent context.Context, job jobs.Job, run automation.Run, assetID string) (result automation.Target) {
	result = automation.Target{AssetID: assetID, State: "failed", Reason: "authorization_denied"}
	v := run.Snapshot
	s := automation.New(a.db)
	q := jobs.New(a.db)
	ctx, cancel := context.WithTimeout(parent, time.Duration(v.TimeoutSeconds)*time.Second)
	defer cancel()
	asset, u, e := a.taskAsset(ctx, run.ActorID, assetID, v.SourceIP, v.Command)
	if e != nil {
		return
	}
	if e = s.DefinitionActive(ctx, v.ID); e != nil {
		return
	}
	trust, e := a.connectivity.Get(ctx, asset.ID)
	if e != nil || trust.Host != asset.Host || trust.Port != asset.Port {
		result.Reason = "host_key_untrusted"
		return
	}
	var credentialVersion int64
	var username string
	var auth ssh.AuthMethod
	e = a.db.Within(ctx, func(c context.Context) error {
		cred, method, e := a.vault.TaskAuth(c, asset.CredentialID, u.ID, job.ID)
		credentialVersion = cred.Version
		username = cred.Username
		auth = method
		return e
	})
	if e != nil {
		result.Reason = "credential_unavailable"
		return
	}
	authorize := func(c context.Context) error {
		latest, _, e := a.taskAsset(c, run.ActorID, assetID, v.SourceIP, v.Command)
		if e != nil {
			return e
		}
		if latest.Version != asset.Version {
			return fault.Denied
		}
		key, e := a.connectivity.Get(c, assetID)
		if e != nil {
			return e
		}
		if key.Version != trust.Version {
			return fault.Denied
		}
		cred, e := a.vault.Get(c, asset.CredentialID)
		if e != nil {
			return e
		}
		if cred.Version != credentialVersion {
			return fault.Denied
		}
		return s.DefinitionActive(c, v.ID)
	}
	var transport connectivity.Dialer
	switch {
	case asset.AgentID != "":
		transport = func(c context.Context, h string, p int) (net.Conn, error) {
			return a.connectivity.DialAgent(ctx, c, asset.AgentID, job.ID, h, p, authorize)
		}
	case asset.JumpID != "":
		transport = a.jumpDialer(ctx, asset.JumpID, u.ID, job.ID)
	case asset.GatewayGroupID != "":
		transport = a.groupDialer(ctx, asset.GatewayGroupID, u.ID, job.ID, authorize)
	case asset.ProxyID != "":
		transport = a.proxyDialer(ctx, asset.ProxyID, u.ID, job.ID)
	}
	dc, stop := context.WithTimeout(ctx, time.Duration(asset.ConnectTimeout)*time.Second)
	client, e := connectivity.ConnectUsing(dc, trust, username, auth, transport)
	stop()
	auth = nil
	if e != nil {
		result.Reason = "ssh_connect_failed"
		if errors.Is(e, connectivity.ErrHostKey) {
			result.Reason = "host_key_mismatch"
		}
		return
	}
	defer client.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer closeOnCancel()
	command, e := client.NewSession()
	if e != nil {
		result.Reason = "ssh_channel_failed"
		return
	}
	defer command.Close()
	if e = authorize(ctx); e != nil {
		return
	}
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := q.Fence(c, job); e != nil {
			return e
		}
		return s.Dispatch(c, job.ID, assetID)
	})
	if e != nil {
		result.State = "cancelled"
		result.Reason = "dispatch_fence_rejected"
		return
	}
	output := &taskOutput{}
	command.Stdout = output
	command.Stderr = output
	defer func() { result.Output, result.Truncated = output.snapshot() }()
	// Persist dispatch intent first. Any error after this point may hide a remote side effect.
	result.State = "unknown"
	result.Reason = "remote_result_lost"
	done := make(chan error, 1)
	go func() { done <- command.Run(v.Command) }()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case e = <-done:
			if ctx.Err() != nil {
				result.Reason = "cancel_or_timeout_result_unknown"
				return
			}
			if e == nil {
				zero := 0
				result.State = "succeeded"
				result.Reason = ""
				result.ExitCode = &zero
				return
			}
			var exit *ssh.ExitError
			if errors.As(e, &exit) {
				code := exit.ExitStatus()
				result.State = "failed"
				result.Reason = "remote_exit_nonzero"
				result.ExitCode = &code
			}
			return
		case <-ctx.Done():
			_ = client.Close()
			<-done
			result.Reason = "cancel_result_unknown"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				result.Reason = "timeout_result_unknown"
			}
			return
		case <-tick.C:
			c, stop := context.WithTimeout(ctx, time.Second)
			e = authorize(c)
			stop()
			if e != nil {
				cancel()
				_ = client.Close()
				<-done
				result.Reason = "authorization_revoked_result_unknown"
				return
			}
		}
	}
}
