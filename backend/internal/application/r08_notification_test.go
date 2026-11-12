package application

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/notification"
	"sync"
	"sync/atomic"
	"testing"
)

func TestR08NotificationOutboxDedupRetryAndSecretMask(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ctx := context.Background()
	var user string
	if e := p.QueryRow(ctx, "SELECT id::text FROM users WHERE username='accept-admin'").Scan(&user); e != nil {
		t.Fatal(e)
	}
	var fail atomic.Bool
	var sent atomic.Int64
	fail.Store(true)
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/cgi-bin/gettoken" {
			_, _ = w.Write([]byte(`{"access_token":"fixture-token"}`))
			return
		}
		if fail.Load() {
			_, _ = w.Write([]byte(`{"errcode":40014}`))
			return
		}
		sent.Add(1)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer vendor.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: vendor.TLS.Certificates[0].Certificate[0]}))
	channel := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "mock WeCom", Type: "wecom", Enabled: true, Secret: "private-test-secret", Config: notification.Config{APIBase: vendor.URL, CorpID: "mock", AgentID: 1, CA: ca}}, admin)
	postID(t, srv, "/api/admin/notifications/bindings", notification.Binding{UserID: user, ChannelID: channel, Address: "admin", Enabled: true}, admin)
	postID(t, srv, "/api/admin/notifications/rules", notification.Rule{Name: "expiry", Enabled: true, Events: []string{"access.expired"}, ChannelIDs: []string{channel}, Mode: "requester"}, admin)
	service := a.notifications()
	// Test events have no resource payload. The unique key is the business idempotency boundary.
	for i := 0; i < 2; i++ {
		if _, e := p.Exec(ctx, `INSERT INTO notification_events(event_key,event_type,actor_id,payload) VALUES('same-event','access.expired',$1,'{"title":"test","text":"test"}') ON CONFLICT(event_key) DO NOTHING`, user); e != nil {
			t.Fatal(e)
		}
	}
	if e := service.Expand(ctx); e != nil {
		t.Fatal(e)
	}
	if e := service.Expand(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := p.QueryRow(ctx, "SELECT count(*) FROM notification_deliveries").Scan(&count); e != nil || count != 1 {
		t.Fatalf("duplicate event expansion %d %v", count, e)
	}
	eligible := func(context.Context, string, string) (bool, error) { return true, nil }
	if e := service.DeliverOne(ctx, eligible); e != nil {
		t.Fatal(e)
	}
	records, e := service.Deliveries(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if records[0].Status != "pending" || records[0].Attempts != 1 {
		t.Fatal(records)
	}
	fail.Store(false)
	if _, e = p.Exec(ctx, "UPDATE notification_deliveries SET next_attempt=now()"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			if e := service.DeliverOne(ctx, eligible); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	records, e = service.Deliveries(ctx)
	if e != nil || records[0].Status != "delivered" || records[0].Attempts != 2 || sent.Load() != 1 {
		t.Fatalf("delivery: %+v sent %d error %v", records, sent.Load(), e)
	}
	channels, e := service.Channels(ctx)
	if e != nil || len(channels) != 1 || !channels[0].HasSecret || channels[0].Secret != "" {
		t.Fatalf("secret exposure %+v %v", channels, e)
	}
	var encrypted []byte
	if e = p.QueryRow(ctx, "SELECT secret FROM notification_channels WHERE id=$1", channel).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if string(encrypted) == "private-test-secret" {
		t.Fatal("plaintext credential stored")
	}
}
