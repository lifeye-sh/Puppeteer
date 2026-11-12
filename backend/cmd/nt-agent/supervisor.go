package main

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/agentupdate"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func supervise(ctx context.Context, c agent.Config, configPath string) error {
	if c.Updates == nil {
		return errors.New("supervisor requires updates configuration with trusted root")
	}
	cfg := *c.Updates
	if e := os.MkdirAll(cfg.StateDir, 0700); e != nil {
		return e
	}
	binary, e := os.Executable()
	if e != nil {
		return e
	}
	binary, e = filepath.Abs(binary)
	if e != nil {
		return e
	}
	if e = agentupdate.Recover(cfg, binary); e != nil {
		return e
	}
	healthPath := filepath.Join(cfg.StateDir, "worker-health.json")
	requestPath := filepath.Join(cfg.StateDir, "request.json")
	drainPath := filepath.Join(cfg.StateDir, "drain.json")
	running := false
	var child *exec.Cmd
	var done chan error
	start := func(path string) error {
		_ = os.Remove(healthPath)
		child = exec.CommandContext(ctx, path, "--config", configPath, "--health-file", healthPath)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if e := child.Start(); e != nil {
			return e
		}
		running = true
		done = make(chan error, 1)
		cmd := child
		result := done
		go func() { result <- cmd.Wait() }()
		return nil
	}
	if e = start(binary); e != nil {
		return e
	}
	defer func() {
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}
	}()
	var pending *agentupdate.Candidate
	checkTick := time.NewTicker(10 * time.Minute)
	defer checkTick.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	check := func(install bool) {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		candidate, err := agentupdate.Check(cctx, cfg)
		if errors.Is(err, agentupdate.ErrNoUpdate) {
			_ = agentupdate.WriteJSON(filepath.Join(cfg.StateDir, "status.json"), agentupdate.Status{State: "up_to_date", CheckedAt: time.Now()})
			return
		}
		if err != nil {
			_ = agentupdate.WriteJSON(filepath.Join(cfg.StateDir, "status.json"), agentupdate.Status{State: "check_failed", Error: err.Error(), CheckedAt: time.Now()})
			return
		}
		_ = agentupdate.WriteJSON(filepath.Join(cfg.StateDir, "status.json"), agentupdate.Status{State: "available", Version: candidate.Release.Version, Sequence: candidate.Release.Sequence, CheckedAt: time.Now()})
		if install || cfg.AutoInstall {
			pending = &candidate
			_ = agentupdate.WriteJSON(drainPath, map[string]bool{"drain": true})
			_ = agentupdate.WriteJSON(filepath.Join(cfg.StateDir, "status.json"), agentupdate.Status{State: "draining", Version: candidate.Release.Version, Sequence: candidate.Release.Sequence, CheckedAt: time.Now()})
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-checkTick.C:
			if pending == nil {
				check(false)
			}
		case <-poll.C:
			if pending != nil {
				continue
			}
			raw, err := os.ReadFile(requestPath)
			if err == nil && len(raw) < 1024 {
				var request struct {
					Action string `json:"action"`
				}
				if json.Unmarshal(raw, &request) == nil {
					_ = os.Remove(requestPath)
					if request.Action == "check" || request.Action == "install" {
						check(request.Action == "install")
					}
				}
			}
		case <-done:
			running = false
			if ctx.Err() != nil {
				return nil
			}
			if pending != nil {
				candidate := *pending
				pending = nil
				_ = os.Remove(drainPath)
				checkCtx, stopCheck := context.WithTimeout(ctx, 2*time.Minute)
				fresh, checkErr := agentupdate.Check(checkCtx, cfg)
				stopCheck()
				if checkErr != nil || fresh.Release.Sequence != candidate.Release.Sequence || fresh.Digest != candidate.Digest {
					_ = agentupdate.WriteJSON(filepath.Join(cfg.StateDir, "status.json"), agentupdate.Status{State: "check_failed", Error: "release changed or expired while draining", CheckedAt: time.Now()})
					if e = start(binary); e != nil {
						return e
					}
					continue
				}
				candidate = fresh
				e = agentupdate.Install(ctx, cfg, candidate, binary, func(checkCtx context.Context, path string) error {
					if err := start(path); err != nil {
						return err
					}
					timer := time.NewTimer(30 * time.Second)
					defer timer.Stop()
					tick := time.NewTicker(200 * time.Millisecond)
					defer tick.Stop()
					var healthySince time.Time
					for {
						select {
						case <-checkCtx.Done():
							_ = child.Process.Kill()
							<-done
							running = false
							return checkCtx.Err()
						case err := <-done:
							running = false
							if err == nil {
								err = errors.New("candidate exited before health")
							}
							return err
						case <-timer.C:
							_ = child.Process.Kill()
							<-done
							running = false
							return errors.New("candidate did not authenticate")
						case <-tick.C:
							if _, err := os.Stat(healthPath); err == nil {
								if healthySince.IsZero() {
									healthySince = time.Now()
								}
								if time.Since(healthySince) >= 5*time.Second {
									return nil
								}
							}
						}
					}
				})
				if e == nil {
					continue
				}
				if running {
					_ = child.Process.Kill()
					<-done
					running = false
				}
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			if e = start(binary); e != nil {
				return e
			}
		}
	}
}
