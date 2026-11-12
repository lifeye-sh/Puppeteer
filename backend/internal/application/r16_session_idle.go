package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/session"
	"time"
)

func (a *App) sessionActivity(l *liveSession, id string) error {
	ctx, cancel := context.WithTimeout(l.ctx, time.Second)
	defer cancel()
	e := a.sessions.Activity(ctx, id)
	if e != nil {
		l.stop(sessionLeaseReason(e, "activity_check_failed"))
	}
	return e
}

func sessionLeaseReason(e error, fallback string) string {
	if errors.Is(e, session.ErrIdle) {
		return "idle_timeout"
	}
	return fallback
}
