// Package pollwait bounds idle polling while allowing local work to wake it.
package pollwait

import (
	"context"
	"time"
)

type Poller struct{ minimum, maximum, delay time.Duration }

func New(minimum, maximum time.Duration) *Poller {
	if minimum <= 0 || maximum < minimum {
		panic("invalid polling bounds")
	}
	return &Poller{minimum, maximum, minimum}
}
func (p *Poller) Record(active bool) {
	if active {
		p.delay = p.minimum
	} else {
		p.delay = min(p.maximum, p.delay*2)
	}
}
func (p *Poller) Wait(ctx context.Context, wake <-chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		p.delay = p.minimum
		return ctx.Err() == nil
	case <-timer.C:
		return ctx.Err() == nil
	}
}
