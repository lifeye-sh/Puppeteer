package agent

import (
	"context"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"testing"
	"time"
)

func TestLeaseExpiryAndTargetBinding(t *testing.T) {
	l := newLeases()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.watch(ctx)
	v := agentwire.Lease{SessionID: "session", Host: "10.0.0.1", Port: 22, ExpiresAt: time.Now().Add(300 * time.Millisecond).UnixMilli()}
	if e := l.update(v); e != nil {
		t.Fatal(e)
	}
	stream, stop := context.WithCancel(ctx)
	defer stop()
	if l.claim(ctx, agentwire.Dial{SessionID: "session", Host: "10.0.0.2", Port: 22}, stop) {
		t.Fatal("target mismatch accepted")
	}
	if !l.claim(ctx, agentwire.Dial{SessionID: "session", Host: v.Host, Port: v.Port}, stop) {
		t.Fatal("valid lease rejected")
	}
	if l.claim(ctx, agentwire.Dial{SessionID: "session", Host: v.Host, Port: v.Port}, stop) {
		t.Fatal("lease reused")
	}
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("expired stream not closed")
	}
	v.ExpiresAt = time.Now().Add(16 * time.Second).UnixMilli()
	if l.update(v) == nil {
		t.Fatal("overlong lease accepted")
	}
}
