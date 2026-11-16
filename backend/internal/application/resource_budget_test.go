package application

import (
	"context"
	"golang.org/x/sync/semaphore"
	"net/http/httptest"
	"puppeteer/backend/internal/platform/jobs"
	"testing"
	"time"
)

func TestResourceDeploymentInternalRoutesRemainLoopbackOnly(t *testing.T) {
	for _, operation := range []string{"resource-drain", "resource-resume"} {
		r := httptest.NewRequest("POST", "http://localhost/api/internal/deployment-"+operation, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Authorization", "Bearer test-token")
		if !internalDeploymentRequest(r) {
			t.Fatal("executor route refused", operation)
		}
		r.RemoteAddr = "192.0.2.1:12345"
		if internalDeploymentRequest(r) {
			t.Fatal("remote internal request allowed")
		}
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Origin", "https://example.test")
		if internalDeploymentRequest(r) {
			t.Fatal("browser internal request allowed")
		}
	}
}

func TestHeavyBudgetWaitingDoesNotClaimBackupLease(t *testing.T) {
	_, a, _ := r02Server(t, false)
	a.heavyTasks = semaphore.NewWeighted(1)
	release, err := a.acquireHeavy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	id, err := jobs.New(a.db).Enqueue(context.Background(), "backup.create", "queued", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err = a.RunBackupOnce(ctx); err == nil {
		t.Fatal("waiting worker ignored cancellation")
	}
	var state string
	var untouched bool
	if err = a.db.Row(context.Background(), `SELECT state,lease_token IS NULL AND started_at IS NULL FROM jobs WHERE id=$1`, id).Scan(&state, &untouched); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || !untouched {
		t.Fatal("waiting consumed a job lease", state, untouched)
	}
}

func TestHeavyBudgetWaitCancelAndDrain(t *testing.T) {
	a := &App{config: Config{ResourceProfile: "light"}, heavyTasks: semaphore.NewWeighted(1)}
	release, err := a.acquireHeavy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.tryHeavy(); ok {
		t.Fatal("retention overlapped heavy work")
	}
	if err = a.drainHeavyForDeployment(); err == nil {
		t.Fatal("deployment interrupted active work")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = a.acquireHeavy(ctx); err == nil {
		t.Fatal("blocked admission did not respect cancellation")
	}
	release()
	if err = a.drainHeavyForDeployment(); err != nil {
		t.Fatal(err)
	}
	if err = a.drainHeavyForDeployment(); err != nil {
		t.Fatal("drain must be idempotent", err)
	}
	if _, ok := a.tryHeavy(); ok {
		t.Fatal("new heavy task admitted during deployment")
	}
	a.resumeHeavyAfterDeployment()
	a.resumeHeavyAfterDeployment()
	release, ok := a.tryHeavy()
	if !ok {
		t.Fatal("admission not restored")
	}
	release()
}

func TestHeavyBudgetPerformanceAllowsTwoAndNoMore(t *testing.T) {
	a := &App{config: Config{ResourceProfile: "performance"}, heavyTasks: semaphore.NewWeighted(2)}
	first, _ := a.acquireHeavy(context.Background())
	second, _ := a.acquireHeavy(context.Background())
	if _, ok := a.tryHeavy(); ok {
		t.Fatal("third heavy task admitted")
	}
	first()
	second()
	if err := a.drainHeavyForDeployment(); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.tryHeavy(); ok {
		t.Fatal("drain reserved only one slot")
	}
	a.resumeHeavyAfterDeployment()
}
