package application

import (
	"context"
	"testing"
	"time"
)

func TestR11MaintenanceDrainGate(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.StartBackgroundServices(life)
	ctx, stop := context.WithTimeout(life, 5*time.Second)
	defer stop()
	leave, e := a.beginMaintenance(ctx)
	if e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", "/api/system/runtime", nil, admin, origin), 503)
	expect(t, call(t, srv.URL, "GET", "/healthz", nil, nil, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/admin/backup/config", nil, admin, origin), 200)
	if _, e = a.beginMaintenance(ctx); e == nil {
		t.Fatal("concurrent maintenance accepted")
	}
	select {
	case <-a.backgroundDone:
	default:
		t.Fatal("workers were not drained")
	}
	leave()
	expect(t, call(t, srv.URL, "GET", "/api/system/runtime", nil, admin, origin), 200)
	cancel()
	if e = a.pauseBackground(ctx); e != nil && e != context.Canceled {
		t.Fatal(e)
	}
}
