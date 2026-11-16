// Package deploytask persists deployment progress independently of the API process.
package deploytask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrBusy = errors.New("another deployment task is running")
var ErrJournal = errors.New("deployment journal unavailable; restore storage and restart executor")
var ErrConflict = errors.New("deployment generation changed; preflight again")

type Request struct {
	RendererMode    string `json:"rendererMode,omitempty"`
	RendererQuality string `json:"rendererQuality,omitempty"`
	ResourceProfile string `json:"resourceProfile,omitempty"`
	AllowCIDRs      string `json:"allowCidrs,omitempty"`
	AdminIP         string `json:"adminIp,omitempty"`
	Repository      string `json:"repository,omitempty"`
	TrustRoot       string `json:"trustRoot,omitempty"`
	SiteID          string `json:"siteId,omitempty"`
	SiteVersion     int64  `json:"siteVersion,omitempty"`
	SiteEnabled     bool   `json:"siteEnabled,omitempty"`
	Actor           string `json:"actor,omitempty"`
	Protocol        string `json:"protocol"`
	Enabled         bool   `json:"enabled"`
	Bind            string `json:"bind"`
	Port            int    `json:"port"`
	Generation      uint64 `json:"generation"`
}
type Task struct {
	ProgressToken string    `json:"progressToken,omitempty"`
	ID            string    `json:"id"`
	Request       Request   `json:"request"`
	State         string    `json:"state"`
	Stage         string    `json:"stage"`
	Error         string    `json:"error,omitempty"`
	Started       time.Time `json:"started"`
	Updated       time.Time `json:"updated"`
}

// Driver is implemented by the host executor. Each operation must be idempotent.
// Preflight must not mutate deployment. Apply must enforce its plan again.
// Fence blocks the selected entry after an interrupted or failed disable.
// Errors returned by drivers must exclude credentials and command output.
type Driver interface {
	Preflight(context.Context, Request) error
	Apply(context.Context, Request) error
	Verify(context.Context, Request) error
	Fence(context.Context, Request) error
	Rollback(context.Context, Request) error
}
type snapshot struct {
	Generation uint64 `json:"generation"`
	Task       *Task  `json:"task,omitempty"`
	History    []Task `json:"history,omitempty"`
}
type Engine struct {
	mu         sync.Mutex
	path       string
	driver     Driver
	data       snapshot
	busy       bool
	journalErr error
}

// Open requires an executor-owned, private state directory. The caller must
// hold a process lock for its lifetime; a second daemon must not share it.
func Open(ctx context.Context, dir string, driver Driver) (*Engine, error) {
	if driver == nil {
		return nil, errors.New("deployment driver required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	e := &Engine{path: filepath.Join(dir, "task.json"), driver: driver}
	raw, err := os.ReadFile(e.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &e.data); err != nil {
			return nil, fmt.Errorf("invalid deployment journal: %w", err)
		}
	}
	if t := e.data.Task; t != nil && t.State == "running" {
		priorStage := t.Stage
		t.State, t.Stage = "failed", "interrupted"
		t.Error = "executor restarted; operation interrupted before deployment"
		if priorStage != "preflight" {
			recovery, cancel := context.WithTimeout(ctx, 8*time.Minute)
			if t.Request.IsDisable() {
				err = driver.Fence(recovery, t.Request)
			} else if priorStage == "verify" && !specialized(t.Request.Protocol) {
				err = driver.Verify(recovery, t.Request)
				if err == nil {
					t.State, t.Stage, t.Error = "succeeded", "complete", ""
				} else {
					err = driver.Rollback(recovery, t.Request)
				}
			} else {
				err = driver.Rollback(recovery, t.Request)
			}
			cancel()
			if err != nil {
				t.Error = "executor restarted; recovery failed: " + err.Error()
			} else if t.State != "succeeded" {
				t.Error = "executor restarted; interrupted operation recovered"
			}
		}
		t.Updated = time.Now().UTC()
		if err = e.persist(); err != nil {
			return nil, err
		}
	}
	return e, nil
}
func (e *Engine) persist() error {
	raw, err := json.Marshal(e.data)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(e.path), ".task-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, e.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(e.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (e *Engine) View() (uint64, *Task) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.data.Task == nil {
		return e.data.Generation, nil
	}
	copy := *e.data.Task
	return e.data.Generation, &copy
}
func (e *Engine) stage(stage, state, message string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data.Task.Stage = stage
	e.data.Task.State = state
	e.data.Task.Error = message
	e.data.Task.Updated = time.Now().UTC()
	if err := e.persist(); err != nil {
		e.journalErr = err
		e.data.Task.State = "failed"
		e.data.Task.Stage = "journal"
		e.data.Task.Error = ErrJournal.Error()
		return ErrJournal
	}
	return nil
}

// Start persists intent before any side effect and returns promptly. ctx must be
// the daemon lifetime, not an HTTP request context. Poll View for progress.
func (e *Engine) Start(ctx context.Context, r Request) (Task, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	if err := Validate(r); err != nil {
		return Task{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.busy {
		return Task{}, ErrBusy
	}
	if e.journalErr != nil {
		return Task{}, ErrJournal
	}
	if r.Generation != e.data.Generation {
		return Task{}, ErrConflict
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Task{}, err
	}
	ticket := make([]byte, 32)
	if _, err := rand.Read(ticket); err != nil {
		return Task{}, err
	}
	t := Task{ProgressToken: hex.EncodeToString(ticket), ID: hex.EncodeToString(id), Request: r, State: "running", Stage: "preflight", Started: time.Now().UTC(), Updated: time.Now().UTC()}
	previous := e.data
	if previous.Task != nil {
		history := append([]Task(nil), previous.History...)
		history = append(history, *previous.Task)
		if len(history) > 100 {
			history = history[len(history)-100:]
		}
		e.data.History = history
	}
	e.data.Generation++
	e.data.Task = &t
	if err := e.persist(); err != nil {
		e.data = previous
		e.journalErr = err
		return Task{}, ErrJournal
	}
	e.busy = true
	result := t
	go e.run(ctx, r)
	return result, nil
}
func (e *Engine) run(ctx context.Context, r Request) {
	defer func() { e.mu.Lock(); e.busy = false; e.mu.Unlock() }()
	if err := e.driver.Preflight(ctx, r); err != nil {
		e.stage("preflight", "failed", err.Error())
		return
	}
	if err := e.stage("apply", "running", ""); err != nil {
		return
	}
	err := e.driver.Apply(ctx, r)
	if err == nil {
		if err = e.stage("verify", "running", ""); err == nil {
			err = e.driver.Verify(ctx, r)
		}
	}
	if err == nil {
		e.stage("complete", "succeeded", "")
		return
	}
	reason := err.Error()
	recovery, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if r.IsDisable() {
		// Recovery must proceed even when the journal disk is unavailable.
		e.stage("fence", "running", reason)
		if fenceErr := e.driver.Fence(recovery, r); fenceErr != nil {
			reason += "; disabling fence failed: " + fenceErr.Error()
		}
	} else {
		e.stage("rollback", "running", reason)
		if rollbackErr := e.driver.Rollback(recovery, r); rollbackErr != nil {
			reason += "; rollback failed: " + rollbackErr.Error()
		}
	}
	e.stage("complete", "failed", reason)
}

// Preflight is serialized with deployments, but does not create a task or mutate
// the generation. Start always repeats it; this result is not an authorization.
func (e *Engine) Preflight(ctx context.Context, r Request) error {
	if err := Validate(r); err != nil {
		return err
	}
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return ErrBusy
	}
	if e.journalErr != nil {
		e.mu.Unlock()
		return ErrJournal
	}
	if e.data.Generation != r.Generation {
		e.mu.Unlock()
		return ErrConflict
	}
	e.busy = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.busy = false; e.mu.Unlock() }()
	return e.driver.Preflight(ctx, r)
}

// Wait lets the daemon finish bounded rollback/fencing before releasing its lock.
func (e *Engine) Wait(ctx context.Context) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		busy := e.busy
		e.mu.Unlock()
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Tasks returns copies, newest first, retaining the latest 100 completed tasks.
func (e *Engine) Tasks() []Task {
	e.mu.Lock()
	defer e.mu.Unlock()
	tasks := make([]Task, 0, len(e.data.History)+1)
	if e.data.Task != nil {
		tasks = append(tasks, *e.data.Task)
	}
	for i := len(e.data.History) - 1; i >= 0; i-- {
		tasks = append(tasks, e.data.History[i])
	}
	return tasks
}
func (e *Engine) Find(id string) *Task {
	for _, task := range e.Tasks() {
		if task.ID == id {
			return &task
		}
	}
	return nil
}

func (e *Engine) Report(stage string) error { return e.stage(stage, "running", "") }

// Scheduled work shares the deployment gate; shutdown waits for it too.
func (e *Engine) Maintain(ctx context.Context, work func(context.Context) error) error {
	e.mu.Lock()
	if e.busy || e.journalErr != nil {
		e.mu.Unlock()
		return ErrBusy
	}
	e.busy = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.busy = false; e.mu.Unlock() }()
	return work(ctx)
}
