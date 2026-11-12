package postgres

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunAdmittedPreflightRevocationAndCancellation(t *testing.T) {
	denied := errors.New("denied")
	var revoke atomic.Bool
	db := New(nil)
	db.SetAdmissionCheck(func(ctx context.Context, category, feature string) error {
		if revoke.Load() {
			return denied
		}
		return nil
	})
	revoke.Store(true)
	if e := db.RunAdmitted(context.Background(), "test", func(context.Context) error { t.Fatal("dispatched denied operation"); return nil }); !errors.Is(e, denied) || !errors.Is(e, ErrAdmission) {
		t.Fatal(e)
	}
	revoke.Store(false)
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Second)
	defer stop()
	e := db.RunAdmitted(ctx, "test", func(c context.Context) error { revoke.Store(true); <-c.Done(); return c.Err() })
	if !errors.Is(e, denied) {
		t.Fatal("revocation not propagated", e)
	}
	revoke.Store(false)
	parent, cancel := context.WithCancel(context.Background())
	e = db.RunAdmitted(parent, "test", func(c context.Context) error { cancel(); return c.Err() })
	if !errors.Is(e, context.Canceled) || errors.Is(e, ErrAdmission) {
		t.Fatal(e)
	}
	e = db.RunAdmitted(context.Background(), "test", func(context.Context) error { revoke.Store(true); return nil })
	if !errors.Is(e, denied) {
		t.Fatal("return check missing", e)
	}
}
