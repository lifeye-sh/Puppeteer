package databaseauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordVerifier(t *testing.T) {
	v := NewVerifier()
	ctx := context.Background()
	password, encoded, e := v.Create(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(password) != 43 || strings.Contains(encoded, password) {
		t.Fatal("unexpected password material")
	}
	if e = v.Verify(ctx, password, encoded); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(v.Verify(ctx, strings.Repeat("x", 43), encoded), ErrInvalid) {
		t.Fatal("wrong password accepted")
	}
	if !errors.Is(v.Verify(ctx, password, strings.Replace(encoded, "65536", "1048576", 1)), ErrInvalid) {
		t.Fatal("untrusted cost accepted")
	}
	if !errors.Is(v.Verify(ctx, password, prefix+"bad$bad"), ErrInvalid) {
		t.Fatal("invalid encoding accepted")
	}
}
func TestVerifierCapacityAndCancellation(t *testing.T) {
	v := NewVerifier()
	for i := 0; i < cap(v.active); i++ {
		v.active <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e := v.acquire(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if len(v.admitted) != 0 {
		t.Fatal("cancelled queue admission leaked")
	}
	for i := 0; i < cap(v.admitted); i++ {
		v.admitted <- struct{}{}
	}
	if _, e := v.acquire(context.Background()); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
}
func TestWireIdentity(t *testing.T) {
	u, a, e := SplitIdentity("alice@reporting")
	if e != nil || u != "alice" || a != "reporting" {
		t.Fatal(u, a, e)
	}
	for _, s := range []string{"alice", "@db", "alice@", "alice@db@other", "alice@db\x00", " alice@db"} {
		if _, _, e = SplitIdentity(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
