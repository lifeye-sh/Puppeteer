package deploytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDriver struct {
	mu    sync.Mutex
	calls []string
	fail  string
	gate  chan struct{}
	hook  func(string)
}

func (d *fakeDriver) call(name string) error {
	d.mu.Lock()
	d.calls = append(d.calls, name)
	d.mu.Unlock()
	if d.hook != nil {
		d.hook(name)
	}
	if name == d.fail {
		return errors.New(name + " failed")
	}
	return nil
}
func (d *fakeDriver) Preflight(ctx context.Context, r Request) error {
	if d.gate != nil {
		<-d.gate
	}
	return d.call("preflight")
}
func (d *fakeDriver) Apply(context.Context, Request) error    { return d.call("apply") }
func (d *fakeDriver) Verify(context.Context, Request) error   { return d.call("verify") }
func (d *fakeDriver) Fence(context.Context, Request) error    { return d.call("fence") }
func (d *fakeDriver) Rollback(context.Context, Request) error { return d.call("rollback") }
func await(t *testing.T, e *Engine) *Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		busy := e.busy
		e.mu.Unlock()
		if !busy {
			_, task := e.View()
			return task
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("task stuck")
	return nil
}
func TestLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, fail, want string
		enabled          bool
	}{
		{"success", "", "preflight,apply,verify", true},
		{"preflight", "preflight", "preflight", true},
		{"enable failure", "verify", "preflight,apply,verify,rollback", true},
		{"disable failure", "apply", "preflight,apply,fence", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDriver{fail: tc.fail}
			dir := t.TempDir()
			e, err := Open(context.Background(), dir, d)
			if err != nil {
				t.Fatal(err)
			}
			r := Request{Protocol: "mysql", Enabled: tc.enabled, Bind: "127.0.0.1", Port: 13306}
			if _, err = e.Start(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			task := await(t, e)
			if (task.State == "succeeded") != (tc.fail == "") {
				t.Fatal(task)
			}
			d.mu.Lock()
			calls := strings.Join(d.calls, ",")
			d.mu.Unlock()
			if calls != tc.want {
				t.Fatal(calls)
			}
			reopened, err := Open(context.Background(), dir, d)
			if err != nil {
				t.Fatal(err)
			}
			gen, saved := reopened.View()
			if gen != 1 || saved.ID != task.ID || saved.State != task.State {
				t.Fatal(gen, saved)
			}
			if _, err = e.Start(context.Background(), r); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}
func TestSerialAndSnapshot(t *testing.T) {
	d := &fakeDriver{gate: make(chan struct{})}
	e, err := Open(context.Background(), t.TempDir(), d)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Protocol: "ssh", Enabled: true, Bind: "0.0.0.0", Port: 2022}
	if _, err = e.Start(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Start(context.Background(), r); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	_, copy := e.View()
	copy.State = "corrupted"
	_, actual := e.View()
	if actual.State != "running" {
		t.Fatal(actual)
	}
	close(d.gate)
	await(t, e)
}
func TestInterruptedDisableFencesWithoutReplay(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(snapshot{Generation: 4, Task: &Task{ID: "interrupted", State: "running", Request: Request{Protocol: "mysql", Enabled: false}}})
	if err := os.WriteFile(filepath.Join(dir, "task.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	d := &fakeDriver{}
	e, err := Open(context.Background(), dir, d)
	if err != nil {
		t.Fatal(err)
	}
	_, task := e.View()
	if task.State != "failed" || task.Stage != "interrupted" || strings.Join(d.calls, ",") != "fence" {
		t.Fatal(task, d.calls)
	}
}
func TestInvalidRequest(t *testing.T) {
	for _, r := range []Request{{Protocol: "shell"}, {Protocol: "mysql", Enabled: true, Bind: "host", Port: 3306}, {Protocol: "mysql", Enabled: true, Bind: "0.0.0.0", Port: 65536}} {
		if Validate(r) == nil {
			t.Fatal(r)
		}
	}
	if Validate(Request{Protocol: "mysql"}) != nil {
		t.Fatal("disable must not require port")
	}
}

func TestSharedWebsiteDisableFailureFencesInsteadOfRollback(t *testing.T) {
	d := &fakeDriver{fail: "apply"}
	e, err := Open(context.Background(), t.TempDir(), d)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Protocol: "website", Enabled: true, Bind: "127.0.0.1", Port: 18443, SiteID: "00000000-0000-4000-8000-000000000001", SiteVersion: 1, SiteEnabled: false}
	if _, err = e.Start(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	await(t, e)
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.Join(d.calls, ",") != "preflight,apply,fence" {
		t.Fatal(d.calls)
	}
}

func TestJournalFailureBlocksFurtherDeploymentsAndStillRecovers(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			dir := t.TempDir()
			d := &fakeDriver{}
			e, err := Open(context.Background(), dir, d)
			if err != nil {
				t.Fatal(err)
			}
			d.hook = func(name string) {
				if name == "apply" {
					// A directory at the journal destination makes the atomic rename fail.
					if err := os.Remove(e.path); err != nil {
						t.Error(err)
					}
					if err := os.Mkdir(e.path, 0700); err != nil {
						t.Error(err)
					}
				}
			}
			r := Request{Protocol: "mysql", Enabled: enabled, Bind: "127.0.0.1", Port: 13306}
			if _, err := e.Start(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			task := await(t, e)
			if task.State != "failed" || task.Stage != "journal" {
				t.Fatal(task)
			}
			recovery := "rollback"
			if !enabled {
				recovery = "fence"
			}
			if strings.Join(d.calls, ",") != "preflight,apply,"+recovery {
				t.Fatal(d.calls)
			}
			r.Generation = 1
			if _, err := e.Start(context.Background(), r); !errors.Is(err, ErrJournal) {
				t.Fatal(err)
			}
			if err := e.Preflight(context.Background(), r); !errors.Is(err, ErrJournal) {
				t.Fatal(err)
			}
		})
	}
}
func TestRestartRecoveryStages(t *testing.T) {
	for _, tc := range []struct{ stage, fail, calls, state string }{
		{"preflight", "", "", "failed"}, {"apply", "", "rollback", "failed"},
		{"verify", "", "verify", "succeeded"}, {"verify", "verify", "verify,rollback", "failed"},
		{"rollback", "", "rollback", "failed"},
	} {
		t.Run(tc.stage+tc.fail, func(t *testing.T) {
			dir := t.TempDir()
			raw, _ := json.Marshal(snapshot{Generation: 1, Task: &Task{ID: "restart", State: "running", Stage: tc.stage, Request: Request{Protocol: "mysql", Enabled: true}}})
			if err := os.WriteFile(filepath.Join(dir, "task.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			d := &fakeDriver{fail: tc.fail}
			e, err := Open(context.Background(), dir, d)
			if err != nil {
				t.Fatal(err)
			}
			_, task := e.View()
			if task.State != tc.state || strings.Join(d.calls, ",") != tc.calls {
				t.Fatal(task, d.calls)
			}
		})
	}
}
func TestHistorySurvivesNextTaskAndRestart(t *testing.T) {
	dir := t.TempDir()
	d := &fakeDriver{}
	e, err := Open(context.Background(), dir, d)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.Start(context.Background(), Request{Protocol: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	await(t, e)
	if _, err = e.Start(context.Background(), Request{Protocol: "ssh", Generation: 1}); err != nil {
		t.Fatal(err)
	}
	await(t, e)
	e, err = Open(context.Background(), dir, d)
	if err != nil {
		t.Fatal(err)
	}
	found := e.Find(first.ID)
	if found == nil || found.State != "succeeded" || len(e.Tasks()) != 2 {
		t.Fatal(e.Tasks())
	}
	found.State = "corrupt"
	if e.Find(first.ID).State != "succeeded" {
		t.Fatal("history is mutable")
	}
}

func TestMaintenanceSharesDeploymentGate(t *testing.T) {
	e, err := Open(context.Background(), t.TempDir(), &fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- e.Maintain(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return errors.New("fixture maintenance failure")
		})
	}()
	<-entered
	if err = e.Preflight(context.Background(), Request{Protocol: "wal"}); !errors.Is(err, ErrBusy) {
		t.Error(err)
	}
	if _, err = e.Start(context.Background(), Request{Protocol: "wal"}); !errors.Is(err, ErrBusy) {
		t.Error(err)
	}
	close(release)
	if err = <-done; err == nil {
		t.Fatal("maintenance error hidden")
	}
	if _, err = e.Start(context.Background(), Request{Protocol: "wal"}); err != nil {
		t.Fatal(err)
	}
	await(t, e)
}

func TestSpecializedRestartRollsBackBeforeLongVerification(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(snapshot{Task: &Task{ID: "wal-restart", State: "running", Stage: "verify", Request: Request{Protocol: "wal", Enabled: true}}})
	if err := os.WriteFile(filepath.Join(dir, "task.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	d := &fakeDriver{}
	if _, err := Open(context.Background(), dir, d); err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.calls, ",") != "rollback" {
		t.Fatal(d.calls)
	}
}
