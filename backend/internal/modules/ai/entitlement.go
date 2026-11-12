package ai

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrExecutionUnavailable = errors.New("AI execution entitlement unavailable")

// History, cancellation, configuration and deletion do not call this guard.
// Admission must also run at the worker/provider boundary, not only at enqueue.
func (s *Service) RequireExecution(ctx context.Context) error {
	if e := s.db.Admit(ctx, "", "ai.assistant"); e != nil {
		return fmt.Errorf("%w: %w", ErrExecutionUnavailable, e)
	}
	return nil
}

func (s *Service) entitledCall(parent context.Context, call func(context.Context) error) error {
	if e := s.RequireExecution(parent); e != nil {
		return e
	}
	ctx, cancel := context.WithCancelCause(parent)
	stopped, done := make(chan struct{}), make(chan struct{})
	defer func() { close(stopped); cancel(nil); <-done }()
	go func() {
		defer close(done)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopped:
				return
			case <-tick.C:
				check, stop := context.WithTimeout(ctx, 2*time.Second)
				e := s.RequireExecution(check)
				stop()
				if e != nil {
					cancel(e)
					return
				}
			}
		}
	}()
	e := call(ctx)
	if cause := context.Cause(ctx); errors.Is(cause, ErrExecutionUnavailable) {
		return cause
	}
	if ctx.Err() != nil {
		if e != nil {
			return e
		}
		return ctx.Err()
	}
	if check := s.RequireExecution(ctx); check != nil {
		return check
	}
	return e
}

func (s *Service) Generate(ctx context.Context, p Profile, messages []Message) (r Reply, e error) {
	r.UsageKnown = true // No request was dispatched if the initial guard rejects.
	e = s.entitledCall(ctx, func(c context.Context) error { var err error; r, err = Generate(c, p, messages); return err })
	return
}
func (s *Service) GenerateStream(ctx context.Context, p Profile, messages []Message, progress func(Reply) error) (r Reply, e error) {
	r.UsageKnown = true
	e = s.entitledCall(ctx, func(c context.Context) error {
		var err error
		r, err = GenerateStream(c, p, messages, progress)
		return err
	})
	return
}
func (s *Service) Models(ctx context.Context, p Profile) (v []string, e error) {
	e = s.entitledCall(ctx, func(c context.Context) error { var err error; v, err = Models(c, p); return err })
	return
}
