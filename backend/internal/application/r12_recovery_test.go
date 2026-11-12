package application

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/pki"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestR12WorkLeaseRecoveryResultsNotificationAndRestore(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("recovery-requester", nil, nil), admin)
	requester := loginAs(t, srv, "recovery-requester")
	postID(t, srv, "/api/admin/users", userInput("recovery-outsider", nil, nil), admin)
	outsider := loginAs(t, srv, "recovery-outsider")
	reader := postID(t, srv, "/api/admin/credentials", credInput("recovery read"), admin)
	writer := postID(t, srv, "/api/admin/credentials", credInput("recovery write"), admin)
	ca, key, e := pki.Generate("recovery target", []string{"db.internal"}, 2, true, nil)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	asset := postID(t, srv, "/api/admin/database-assets", map[string]any{"name": "Recovery target", "alias": "recovery_target", "host": "db.internal", "port": 3306, "database": "inventory", "readonlyCredentialId": reader, "writeCredentialId": writer, "tlsServerName": "db.internal", "tlsCa": string(ca), "enabled": true, "connectTimeout": 10}, admin)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset}, admin)
	// SSH policies must never silently masquerade as database policies.
	expect(t, call(t, srv.URL, "POST", "/api/admin/grants", map[string]any{"userId": user, "resourceId": asset, "strategyId": asset}, admin, origin), 422)
	submit := func() string {
		return postID(t, srv, "/api/database-work-orders", map[string]any{"assetId": asset, "title": "Lease recovery", "sql": "SELECT 1"}, requester)
	}
	approve := func(id string) {
		expect(t, call(t, srv.URL, "POST", "/api/database-work-orders/"+id+"/review", map[string]any{"version": 1, "approve": true}, admin, origin), 204)
	}
	service := database.New(a.db)
	id := submit()
	approve(id)
	work, e := service.ClaimWork(ctx)
	if e != nil || work.ID != id {
		t.Fatal(e)
	}
	if e = service.SaveWorkResult(ctx, id, 0, "pending_commit", 0, []byte(`{"columns":["value"],"rows":[["first"],["second"],["third"]],"total":3}`), time.Millisecond); e != nil {
		t.Fatal(e)
	}
	result := object(t, call(t, srv.URL, "GET", "/api/database-work-orders/"+id+"/results?current=2&pageSize=1", nil, requester, origin))
	data := result["items"].([]any)[0].(map[string]any)["data"].(map[string]any)
	if data["retainedRows"] != float64(3) || data["rows"].([]any)[0].([]any)[0] != "second" {
		t.Fatal("result row paging failed")
	}
	expect(t, call(t, srv.URL, "GET", "/api/database-work-orders/"+id+"/results", nil, outsider, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/database-sql-logs", nil, requester, origin), 403)
	query, e := service.BeginWorkQuery(ctx, id, "SELECT 1")
	if e != nil {
		t.Fatal(e)
	}
	if e = service.FinishQuery(ctx, query, false, 1, time.Millisecond); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE database_work_orders SET heartbeat_at=now()-interval '1 minute' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = a.expireDatabaseWorkLeases(ctx); e != nil {
			t.Fatal(e)
		}
	}
	recovered, e := service.Work(ctx, id, false)
	if e != nil || recovered.State != "unknown" {
		t.Fatal("lease outcome", recovered.State, e)
	}
	if _, e = service.ClaimWork(ctx); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("uncertain operation retried", e)
	}
	var logState string
	if e = pool.QueryRow(ctx, `SELECT state FROM database_sql_logs WHERE id=$1`, query).Scan(&logState); e != nil || logState != "unknown" {
		t.Fatal("audit not quarantined", logState, e)
	}
	results, e := service.WorkResults(ctx, id, 0, 1)
	if e != nil || results[0].State != "unknown" {
		t.Fatal("provisional result not quarantined", e)
	}
	logs := object(t, call(t, srv.URL, "GET", "/api/admin/database-sql-logs", nil, admin, origin))
	if logs["total"] != float64(1) {
		t.Fatal("work audit missing from HTTP view")
	}
	// Deliver the actual outbox event over an isolated TLS SMTP socket.
	host, port, smtpCA, messages := r12SMTPFixture(t)
	channel := postID(t, srv, "/api/admin/notifications/channels", notification.Channel{Name: "R12 internal SMTP", Type: "smtp", Enabled: true, Config: notification.Config{Host: host, Port: port, From: "platform@example.test", CA: smtpCA, ImplicitTLS: true}}, admin)
	postID(t, srv, "/api/admin/notifications/bindings", notification.Binding{UserID: user, ChannelID: channel, Address: "requester@example.test", Enabled: true}, admin)
	postID(t, srv, "/api/admin/notifications/rules", notification.Rule{Name: "R12 finished", Enabled: true, Events: []string{"database.finished"}, ChannelIDs: []string{channel}, Mode: "requester"}, admin)
	for range 10 {
		if e = a.notifications().Expand(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if e = a.notifications().DeliverOne(ctx, a.notificationEligible); e != nil {
		t.Fatal(e)
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, work.RunID) || !strings.Contains(message, "unknown") || !strings.Contains(message, work.Hash) || strings.Contains(message, "SELECT 1") {
			t.Fatal("notification correlation or redaction failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP delivery did not arrive")
	}
	deliveries, e := a.notifications().Deliveries(ctx)
	if e != nil || len(deliveries) != 1 || deliveries[0].Status != "delivered" {
		t.Fatal("delivery not recorded", e)
	}
	// A restored archive cannot restart native sessions or queued/running SQL.
	pending := submit()
	running := submit()
	approve(running)
	claimed, e := service.ClaimWork(ctx)
	if e != nil || claimed.ID != running {
		t.Fatal(e)
	}
	logged, e := service.BeginWorkQuery(ctx, running, "SELECT 1")
	if e != nil {
		t.Fatal(e)
	}
	if e = service.FinishQuery(ctx, logged, false, 1, time.Millisecond); e != nil {
		t.Fatal(e)
	}
	v, e := service.Asset(ctx, asset)
	if e != nil {
		t.Fatal(e)
	}
	session, e := service.BeginSession(ctx, user, v, 1, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.QuarantineRestoredState(ctx, "r12-isolated-restore"); e != nil {
		t.Fatal(e)
	}
	for target, want := range map[string]string{pending: "cancelled", running: "unknown"} {
		v, e := service.Work(ctx, target, false)
		if e != nil || v.State != want {
			t.Fatal("restored SQL state", v.State, want, e)
		}
	}
	var active int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM database_sessions WHERE id=$1 AND ended_at IS NULL`, session).Scan(&active); e != nil || active != 0 {
		t.Fatal("restored native session remained active", e)
	}
	if e = pool.QueryRow(ctx, `SELECT state FROM database_sql_logs WHERE id=$1`, logged).Scan(&logState); e != nil || logState != "unknown" {
		t.Fatal("restored uncommitted audit remained completed", e)
	}
}

func r12SMTPFixture(t *testing.T) (string, int, string, <-chan string) {
	t.Helper()
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(cert.Close)
	listener, e := tls.Listen("tcp", "127.0.0.1:0", cert.TLS)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	messages := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 fixture ESMTP\r\n")
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250 fixture\r\n")
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(conn, "354 Send message\r\n")
				var message strings.Builder
				for {
					line, e = reader.ReadString('\n')
					if e != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					message.WriteString(line)
				}
				fmt.Fprint(conn, "250 accepted\r\n")
				messages <- message.String()
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.TLS.Certificates[0].Certificate[0]}))
	return host, number, ca, messages
}
