package notification

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/postgres"
	"next-terminal/backend/internal/platform/secretbox"
	"slices"
	"time"
)

// Eligibility is supplied by the approval application, and is rechecked before delivery.
type Eligibility func(context.Context, string, string) (bool, error)

func (s *Service) Expand(ctx context.Context) error {
	return s.db.Within(ctx, func(ctx context.Context) error {
		var id, event, actor string
		var reviewers []string
		e := s.db.Row(ctx, `SELECT id::text,event_type,actor_id::text,reviewer_ids FROM notification_events WHERE NOT expanded ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &event, &actor, &reviewers)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		rules, e := s.Rules(ctx)
		if e != nil {
			return e
		}
		for _, rule := range rules {
			if !rule.Enabled || !slices.Contains(rule.Events, event) {
				continue
			}
			users := rule.RecipientIDs
			if rule.Mode == "requester" {
				users = []string{actor}
			} else if rule.Mode == "reviewers" {
				users = reviewers
			}
			for _, user := range users {
				for _, channel := range rule.ChannelIDs {
					_, e = s.db.Exec(ctx, `INSERT INTO notification_deliveries(event_id,rule_id,channel_id,recipient_id) SELECT $1,$2,id,$4 FROM notification_channels WHERE id=$3 AND enabled ON CONFLICT DO NOTHING`, id, rule.ID, channel, user)
					if e != nil {
						return e
					}
				}
			}
		}
		_, e = s.db.Exec(ctx, `UPDATE notification_events SET expanded=true WHERE id=$1`, id)
		return e
	})
}

type Delivery struct {
	Test        bool      `json:"test"`
	ID          string    `json:"id"`
	ChannelID   string    `json:"channelId"`
	RecipientID string    `json:"recipientId"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	Error       string    `json:"error"`
	CreatedAt   time.Time `json:"createdAt"`
	NextAttempt time.Time `json:"nextAttempt"`
}

func (s *Service) Deliveries(ctx context.Context) ([]Delivery, error) {
	rows, e := s.db.Rows(ctx, `SELECT id::text,channel_id::text,recipient_id::text,status,attempts,last_error,created_at,next_attempt,test FROM notification_deliveries ORDER BY created_at DESC LIMIT 200`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var v Delivery
		if e = rows.Scan(&v.ID, &v.ChannelID, &v.RecipientID, &v.Status, &v.Attempts, &v.Error, &v.CreatedAt, &v.NextAttempt, &v.Test); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Retry(ctx context.Context, id string) error {
	if e := s.db.RequireFeature(ctx, "notification.send"); e != nil {
		return e
	}
	_, e := s.db.Exec(ctx, `UPDATE notification_deliveries SET status='pending',attempts=0,next_attempt=now(),last_error='' WHERE id=$1 AND status='failed'`, id)
	return e
}

// QuietUntil uses wall clock minutes and handles windows crossing midnight and DST.
func QuietUntil(p Preference, now time.Time) time.Time {
	if p.QuietStart == "" || p.QuietEnd == "" || p.QuietStart == p.QuietEnd {
		return time.Time{}
	}
	zone, e := time.LoadLocation(p.Timezone)
	if e != nil {
		return now.Add(time.Hour)
	}
	quiet := func(t time.Time) bool {
		clock := t.In(zone).Format("15:04")
		if p.QuietStart < p.QuietEnd {
			return clock >= p.QuietStart && clock < p.QuietEnd
		}
		return clock >= p.QuietStart || clock < p.QuietEnd
	}
	if !quiet(now) {
		return time.Time{}
	}
	t := now.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 26*60; i++ {
		if !quiet(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return t
}

func (s *Service) DeliverOne(ctx context.Context, eligible Eligibility) error {
	var id, user, channel, token string
	var attempts int
	var test bool
	e := s.db.Row(ctx, `WITH candidate AS (SELECT id FROM notification_deliveries WHERE (status='pending' AND next_attempt<=now()) OR (status='sending' AND lease_until<now()) ORDER BY next_attempt LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE notification_deliveries d SET status='sending',attempts=attempts+1,last_attempt_at=now(),lease_token=gen_random_uuid()::text,lease_until=now()+interval '90 seconds' FROM candidate c WHERE d.id=c.id RETURNING d.id::text,d.recipient_id::text,d.channel_id::text,d.lease_token,d.attempts,d.test`).Scan(&id, &user, &channel, &token, &attempts, &test)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	finish := func(status, reason string, next time.Time) error {
		_, e := s.db.Exec(ctx, `UPDATE notification_deliveries SET status=$3,last_error=$4,next_attempt=$5,attempts=CASE WHEN $4='quiet hours' THEN greatest(0,attempts-1) ELSE attempts END,lease_until=NULL,lease_token='',sent_at=CASE WHEN $3='delivered' THEN now() ELSE sent_at END WHERE id=$1 AND lease_token=$2 AND status='sending'`, id, token, status, reason, next)
		return e
	}
	fail := func(reason string) error {
		status := "pending"
		if attempts >= 5 {
			status = "failed"
		}
		delay := time.Duration(1<<min(attempts, 8)) * 15 * time.Second
		return finish(status, reason, time.Now().Add(delay))
	}
	if e = s.db.RequireFeature(ctx, "notification.send"); e != nil {
		return finish("skipped", "entitlement unavailable; not dispatched", time.Now())
	}
	subject, e := identity.LoadSubject(ctx, s.db, user)
	if e != nil {
		return fail("recipient lookup failed")
	}
	if !subject.Enabled {
		return finish("skipped", "recipient disabled", time.Now())
	}
	if !test {
		if eligible == nil {
			return fail("eligibility service unavailable")
		}
		ok, e := eligible(ctx, id, user)
		if e != nil {
			return fail("eligibility check failed")
		}
		if !ok {
			return finish("skipped", "recipient no longer eligible", time.Now())
		}
	}
	pref, e := s.Preference(ctx, user)
	if e != nil {
		return fail("preference lookup failed")
	}
	if !pref.Enabled {
		return finish("skipped", "recipient opted out", time.Now())
	}
	if until := QuietUntil(pref, time.Now()); !until.IsZero() {
		return finish("pending", "quiet hours", until)
	}
	var kind, address, aad string
	var config, sealed []byte
	e = s.db.Row(ctx, `SELECT c.type,c.config,coalesce(s.sealed,c.secret),b.address,coalesce('settings:'||s.key||':'||s.id::text,'notification:'||c.id::text) FROM notification_channels c JOIN notification_bindings b ON b.channel_id=c.id AND b.user_id=$2 LEFT JOIN configuration_secrets s ON s.id=c.configuration_secret_id WHERE c.id=$1 AND c.enabled AND b.enabled`, channel, user).Scan(&kind, &config, &sealed, &address, &aad)
	if errors.Is(e, pgx.ErrNoRows) {
		return finish("skipped", "channel or binding disabled", time.Now())
	}
	if e != nil {
		return fail("channel lookup failed")
	}
	var c Config
	if json.Unmarshal(config, &c) != nil {
		return fail("invalid channel configuration")
	}
	var secret []byte
	if len(sealed) > 0 {
		secret, e = secretbox.Open(s.key, sealed, aad)
		if e != nil {
			return fail("channel secret unavailable")
		}
		defer clear(secret)
	}
	title, body := "Puppeteer 通知测试", "这是一条内网通知渠道测试消息。"
	if !test {
		var payload []byte
		if e = s.db.Row(ctx, `SELECT e.payload FROM notification_events e JOIN notification_deliveries d ON d.event_id=e.id WHERE d.id=$1`, id).Scan(&payload); e != nil {
			return fail("event lookup failed")
		}
		var p map[string]string
		if json.Unmarshal(payload, &p) != nil {
			return fail("invalid event")
		}
		title, body = p["title"], p["text"]
	}
	sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	e = s.db.RunAdmitted(sendCtx, "notification.send", func(sendCtx context.Context) error {
		switch kind {
		case "smtp":
			return sendSMTP(sendCtx, c, string(secret), address, id, title, body)
		case "sms-webhook":
			return sendSMS(sendCtx, c, string(secret), address, id, title+"\n"+body)
		case "wecom":
			return sendWeCom(sendCtx, c, string(secret), address, title+"\n"+body)
		default:
			return errTransport
		}
	})
	if errors.Is(e, postgres.ErrAdmission) {
		return finish("skipped", "entitlement changed; delivery result unknown; not retried", time.Now())
	}
	if e != nil {
		return fail("provider delivery failed; check channel TLS, credentials and recipient")
	}
	return finish("delivered", "", time.Now())
}
