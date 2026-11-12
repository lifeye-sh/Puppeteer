package application

import (
	"context"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"time"
)

func (a *App) observerManager(r *http.Request) (session.Session, *identity.User, error) {
	u, e := a.authenticated(r)
	if e != nil {
		return session.Session{}, nil, e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return session.Session{}, u, fault.Invalid
	}
	v, e := a.sessions.Get(r.Context(), id)
	if e != nil {
		return v, u, e
	}
	if v.ActorID != u.ID && u.Type != "admin" {
		return v, u, fault.Denied
	}
	return v, u, nil
}
func activeObservation(v session.Session) bool {
	return !v.Forwarding && v.State == "connected" && v.ClosedAt == nil && !v.Terminate && v.LeaseExpiresAt.After(time.Now())
}
func (a *App) sessionShares(w http.ResponseWriter, r *http.Request) error {
	v, _, e := a.observerManager(r)
	if e != nil {
		return e
	}
	out, e := a.sessions.Shares(r.Context(), v.ID)
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
	return nil
}
func (a *App) createSessionShare(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.observerManager(r)
	if e != nil {
		return e
	}
	if enabled, err := a.audit.RecordingEnabled(r.Context(), v.ID); err != nil {
		return err
	} else if !enabled {
		return problem(409, "recording_disabled", "此会话未启用录屏，无法提供基于录屏流的观察或分享")
	}
	if !activeObservation(v) {
		return fault.Conflict
	}
	if e = a.enforcePolicy(r.Context(), u, v.AssetID, sourceIP(r), "share", ""); e != nil {
		return e
	}
	if _, e = a.sessionAsset(r.Context(), u, v.AssetID); e != nil {
		return e
	}
	var in struct {
		Username string `json:"username"`
		Minutes  int    `json:"minutes"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Username) < 1 || len(in.Username) > 100 || in.Minutes < 1 || in.Minutes > 30 {
		return fault.Invalid
	}
	if !a.allowAttempt("share:" + u.ID) {
		return problem(429, "rate_limited", "分享请求过于频繁")
	}
	var recipient identity.User
	e = a.db.Row(r.Context(), `SELECT id,username,nickname,type FROM users WHERE username=$1 AND status='enabled' AND deleted_at IS NULL`, in.Username).Scan(&recipient.ID, &recipient.Username, &recipient.Nickname, &recipient.Type)
	if e != nil {
		return fault.Denied
	}
	if _, e = a.sessionAsset(r.Context(), &recipient, v.AssetID); e != nil {
		return fault.Denied
	}
	ticket := identity.NewToken()
	var share session.Share
	e = a.db.Within(r.Context(), func(c context.Context) error {
		var e error
		share, e = a.sessions.CreateShare(c, v.ID, u.ID, recipient.ID, identity.Digest(ticket), in.Minutes)
		if e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "session.share_created", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID, "shareId": share.ID, "recipientId": recipient.ID})
	})
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"id": share.ID, "expiresAt": share.ExpiresAt, "path": "/session-observer?sessionId=" + v.ID + "#ticket=" + ticket})
	return nil
}
func (a *App) revokeSessionShare(w http.ResponseWriter, r *http.Request) error {
	v, u, e := a.observerManager(r)
	if e != nil {
		return e
	}
	share := r.PathValue("share")
	if !resource.ValidID(share) {
		return fault.Invalid
	}
	e = a.db.Within(r.Context(), func(c context.Context) error {
		if e := a.sessions.RevokeShare(c, v.ID, share); e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "session.share_revoked", requestID(r), sourceIP(r), map[string]string{"sessionId": v.ID, "shareId": share})
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) joinObserver(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	v, e := a.sessions.Get(r.Context(), id)
	if e != nil {
		return e
	}
	if enabled, err := a.audit.RecordingEnabled(r.Context(), v.ID); err != nil {
		return err
	} else if !enabled {
		return problem(409, "recording_disabled", "此会话未启用录屏，无法提供基于录屏流的观察或分享")
	}
	if !activeObservation(v) {
		return fault.Conflict
	}
	if e = a.enforcePolicy(r.Context(), u, v.AssetID, sourceIP(r), "observe", ""); e != nil {
		return e
	}
	var in struct {
		Ticket string `json:"ticket"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	hash := ""
	if in.Ticket != "" {
		if len(in.Ticket) != 64 {
			return fault.Invalid
		}
		if _, e = a.sessionAsset(r.Context(), u, v.AssetID); e != nil {
			return e
		}
		hash = identity.Digest(in.Ticket)
	} else if u.Type != "admin" && u.ID != v.ActorID {
		return fault.Denied
	}
	if !a.allowAttempt("observe:" + u.ID) {
		return problem(429, "rate_limited", "观察请求过于频繁")
	}
	var observer string
	e = a.db.Within(r.Context(), func(c context.Context) error {
		var e error
		observer, e = a.sessions.Observe(c, id, u.ID, identity.Digest(token(r)), hash)
		if e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "session.observer_joined", requestID(r), sourceIP(r), map[string]string{"sessionId": id, "observerId": observer})
	})
	if e != nil {
		return e
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"id": observer, "protocol": v.Protocol, "watermark": v.Watermark})
	return nil
}
func (a *App) observerEvents(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id, observer := r.PathValue("id"), r.PathValue("observer")
	if !resource.ValidID(id) || !resource.ValidID(observer) {
		return fault.Invalid
	}
	shared, e := a.sessions.Observer(ctx, id, observer, u.ID, identity.Digest(token(r)))
	if e != nil {
		return e
	}
	v, e := a.sessions.Get(ctx, id)
	if e != nil {
		return e
	}
	if shared || u.Type != "admin" {
		if _, e = a.sessionAsset(ctx, u, v.AssetID); e != nil {
			return e
		}
	}
	if !shared && u.Type != "admin" && v.ActorID != u.ID {
		return fault.Denied
	}
	if v.ClosedAt == nil && !activeObservation(v) {
		return fault.Denied
	}
	after := int64(0)
	if r.URL.Query().Has("after") {
		after, e = strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if e != nil || after < 0 {
			return fault.Invalid
		}
	}
	if e = a.enforcePolicy(ctx, u, v.AssetID, sourceIP(r), "observe", ""); e != nil {
		return e
	}
	page, e := a.audit.Observe(ctx, id, after)
	if e != nil {
		return e
	}
	if after > page.Total {
		return fault.Invalid
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, 200, page)
	return nil
}
func (a *App) closeObserver(w http.ResponseWriter, r *http.Request) error {
	u, e := a.authenticated(r)
	if e != nil {
		return e
	}
	id, observer := r.PathValue("id"), r.PathValue("observer")
	if !resource.ValidID(id) || !resource.ValidID(observer) {
		return fault.Invalid
	}
	e = a.db.Within(r.Context(), func(c context.Context) error {
		if e := a.sessions.CloseObserver(c, id, observer, u.ID, identity.Digest(token(r))); e != nil {
			return e
		}
		return a.audit.Detail(c, u.ID, "session.observer_left", requestID(r), sourceIP(r), map[string]string{"sessionId": id, "observerId": observer})
	})
	if e != nil {
		return e
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) routesR07(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/sessions/{id}/shares", a.sessionShares)
	h("POST /api/sessions/{id}/shares", a.createSessionShare)
	h("DELETE /api/sessions/{id}/shares/{share}", a.revokeSessionShare)
	h("POST /api/sessions/{id}/observers", a.joinObserver)
	h("GET /api/sessions/{id}/observers/{observer}/events", a.observerEvents)
	h("DELETE /api/sessions/{id}/observers/{observer}", a.closeObserver)
}
