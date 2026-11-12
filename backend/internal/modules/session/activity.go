package session

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

var ErrIdle = errors.New("session idle timeout")

// Activity is called only after input authorization and parsing, before forwarding
// accepted user input. Client timestamps, heartbeat, resize, remote output and
// observer traffic never extend this deadline. It cannot revive an expired lease.
func (s *Service) Activity(ctx context.Context, id string) error {
	return s.db.Within(ctx, func(c context.Context) error { return s.checkIdle(c, id, true) })
}

func (s *Service) checkIdle(ctx context.Context, id string, touch bool) error {
	policy, e := system.New(s.db).SecurityConfiguration(ctx)
	if e != nil {
		return e
	}
	seconds := policy.Values.Int("access-max-idle-second")
	var alive, expired bool
	e = s.db.Row(ctx, `SELECT state IN ('connecting','connected') AND closed_at IS NULL AND NOT terminate_requested AND lease_expires_at>clock_timestamp() AND quota_epoch=(SELECT auth_epoch FROM instance_state WHERE singleton=1),
 $2::int>0 AND last_activity_at<=clock_timestamp()-make_interval(secs=>$2::int)
 FROM sessions WHERE id=$1 FOR UPDATE`, id, seconds).Scan(&alive, &expired)
	if e != nil {
		return e
	}
	if !alive {
		return fault.Denied
	}
	if expired {
		return ErrIdle
	}
	if touch {
		_, e = s.db.Exec(ctx, `UPDATE sessions SET last_activity_at=clock_timestamp() WHERE id=$1`, id)
	}
	return e
}
