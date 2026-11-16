package pollwait

import (
	"context"
	"testing"
	"time"
)

func TestIdleBackoffWakeAndCancellation(t *testing.T) {
	p := New(time.Second, 15*time.Second)
	for _, want := range []time.Duration{2, 4, 8, 15, 15} {
		p.Record(false)
		if p.delay != want*time.Second {
			t.Fatal(p.delay)
		}
	}
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !p.Wait(ctx, wake) || p.delay != time.Second {
		t.Fatal("local work did not wake idle worker")
	}
	p.Record(false)
	p.Record(true)
	if p.delay != time.Second {
		t.Fatal("activity did not reset backoff")
	}
	cancel()
	if p.Wait(ctx, wake) {
		t.Fatal("cancelled worker resumed")
	}
}
