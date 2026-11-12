package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/entitlements"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestR14NativeRDPTicketStorage(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cred := postID(t, srv, "/api/admin/credentials", credInput("rdp-ticket-test"), admin)
	input := assetInput("rdp-ticket-target", cred, "")
	input["protocol"] = "rdp"
	input["port"] = 3389
	input["certFingerprint"] = "sha256:" + strings.Repeat("ab", 32)
	asset := postID(t, srv, "/api/admin/assets", input, admin)
	var actor string
	if e := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username='accept-admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	worker := "00000000-0000-4000-8000-000000000001"
	reserve := func(protocol string) string {
		t.Helper()
		var id string
		e := a.db.Within(context.Background(), func(ctx context.Context) error {
			var e error
			id, e = a.sessions.Reserve(ctx, session.Session{ActorID: actor, AssetID: asset, Protocol: protocol, ClientIP: "192.0.2.20", AssetVersion: 1, CredentialVersion: 1, HostKeyVersion: 1}, "browser-ticket", "auth")
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	consume := func(id, owner, proof string) error {
		return a.db.Within(context.Background(), func(ctx context.Context) error {
			_, e := a.sessions.ConsumeNativeRDPTicket(ctx, id, owner, proof, worker)
			return e
		})
	}
	for _, mode := range []string{"concurrent", "expired", "expired-lease", "license", "epoch", "terminated", "wrong-owner", "wrong-secret", "ssh", "rollback", "revoked-family", "app-success", "app-ip", "app-revoked", "restore"} {
		t.Run(mode, func(t *testing.T) {
			access := nativeFixtureToken(t, a, admin, []string{"assets:read", "sessions:connect"})
			var family string
			if e := pool.QueryRow(context.Background(), `SELECT family_id FROM oauth_tokens WHERE token_hash=$1`, identity.Digest(access)).Scan(&family); e != nil {
				t.Fatal(e)
			}
			protocol := "rdp"
			if mode == "ssh" {
				protocol = "ssh"
			}
			id := reserve(protocol)
			ticket := "NTICKET:" + id + ":" + strings.Repeat("a", 64)
			digest := a.nativeRDPTicketHash(ticket)
			if digest == a.nativeTicketHash(ticket) {
				t.Fatal("ticket purpose not separated")
			}
			defer pool.Exec(context.Background(), `DELETE FROM sessions WHERE id=$1`, id)
			e := a.db.Within(context.Background(), func(ctx context.Context) error { return a.sessions.IssueNativeRDPTicket(ctx, id, digest, family) })
			if mode == "ssh" {
				if e == nil {
					t.Fatal("SSH ticket kind confusion")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = a.sessions.IssueNativeRDPTicket(context.Background(), id, digest, family); e == nil {
				t.Fatal("transaction not required")
			}
			if strings.HasPrefix(mode, "app-") {
				ip := "192.0.2.20"
				if mode == "app-ip" {
					ip = "192.0.2.21"
				}
				if mode == "app-revoked" {
					if _, e = pool.Exec(context.Background(), `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family); e != nil {
						t.Fatal(e)
					}
				}
				got, f, e := a.consumeNativeRDPTicket(context.Background(), ticket, worker, ip)
				if mode != "app-success" {
					if e == nil {
						t.Fatal("invalid authorization accepted")
					}
					return
				}
				if e != nil || got.ID != id || f != family {
					t.Fatalf("consume application: %v", e)
				}
				if _, _, e = a.consumeNativeRDPTicket(context.Background(), ticket, worker, ip); e == nil {
					t.Fatal("application replay")
				}
				if _, e = a.renewNativeRDPSession(context.Background(), id, worker); e != nil {
					t.Fatal("renew", e)
				}
				if _, e = a.renewNativeRDPSession(context.Background(), id, "00000000-0000-4000-8000-000000000002"); e == nil {
					t.Fatal("different worker renewed")
				}
				if _, e = pool.Exec(context.Background(), `UPDATE sessions SET last_activity_at=clock_timestamp()-interval '20 minutes' WHERE id=$1`, id); e != nil {
					t.Fatal(e)
				}
				if _, e = a.renewNativeRDPSession(context.Background(), id, worker); !errors.Is(e, session.ErrIdle) {
					t.Fatal("native RDP heartbeat extended idle session", e)
				}
				if _, e = pool.Exec(context.Background(), `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family); e != nil {
					t.Fatal(e)
				}
				if _, e = a.renewNativeRDPSession(context.Background(), id, worker); e == nil {
					t.Fatal("revoked session renewed")
				}
				var audits int
				if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action='native.rdp_ticket_consumed'`).Scan(&audits); e != nil || audits != 1 {
					t.Fatalf("audit %d: %v", audits, e)
				}
				return
			}
			if mode == "restore" {
				if e = a.db.Within(context.Background(), func(ctx context.Context) error { return a.sessions.QuarantineRestore(ctx) }); e != nil {
					t.Fatal(e)
				}
				var count int
				if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM native_rdp_tickets WHERE session_id=$1`, id).Scan(&count); e != nil || count != 0 {
					t.Fatalf("restore ticket count %d: %v", count, e)
				}
				if consume(id, actor, digest) == nil {
					t.Fatal("restored ticket accepted")
				}
				return
			}
			if mode == "revoked-family" {
				if _, e := pool.Exec(context.Background(), `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family); e != nil {
					t.Fatal(e)
				}
				if consume(id, actor, digest) == nil {
					t.Fatal("revoked family")
				}
				return
			}
			if mode == "expired" {
				pool.Exec(context.Background(), `UPDATE native_rdp_tickets SET expires_at=now()-interval '1 second' WHERE session_id=$1`, id)
			}
			if mode == "license" {
				local, e := entitlements.NewLocal([]byte(`{"features":{},"quotas":{"users":null,"resources":null,"sessions":null}}`))
				if e != nil {
					t.Fatal(e)
				}
				config := a.config
				config.EntitlementLocal = local
				limited := New(pool, config)
				e = limited.db.Within(context.Background(), func(ctx context.Context) error {
					_, e := limited.sessions.ConsumeNativeRDPTicket(ctx, id, actor, digest, worker)
					return e
				})
				if !errors.Is(e, entitlements.ErrDenied) {
					t.Fatal("native RDP bypassed license", e)
				}
				return
			}
			if mode == "expired-lease" {
				if _, e = pool.Exec(context.Background(), `UPDATE sessions SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "epoch" {
				if _, e = pool.Exec(context.Background(), `UPDATE sessions SET quota_epoch=quota_epoch-1 WHERE id=$1`, id); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "terminated" {
				pool.Exec(context.Background(), `UPDATE sessions SET terminate_requested=true WHERE id=$1`, id)
			}
			if mode == "wrong-owner" {
				if consume(id, worker, digest) == nil {
					t.Fatal("wrong owner")
				}
				return
			}
			if mode == "wrong-secret" {
				if consume(id, actor, strings.Repeat("b", 64)) == nil {
					t.Fatal("wrong secret")
				}
				return
			}
			if mode == "expired" || mode == "terminated" || mode == "expired-lease" || mode == "epoch" {
				if consume(id, actor, digest) == nil {
					t.Fatal("invalid ticket")
				}
				return
			}
			if mode == "rollback" {
				e = a.db.Within(context.Background(), func(ctx context.Context) error {
					if _, e := a.sessions.ConsumeNativeRDPTicket(ctx, id, actor, digest, worker); e != nil {
						return e
					}
					return context.Canceled
				})
				if e == nil {
					t.Fatal("expected rollback")
				}
				if e = consume(id, actor, digest); e != nil {
					t.Fatal(e)
				}
				return
			}
			var wg sync.WaitGroup
			var winners atomic.Int32
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if consume(id, actor, digest) == nil {
						winners.Add(1)
					}
				}()
			}
			wg.Wait()
			if winners.Load() != 1 {
				t.Fatalf("consumption winners %d", winners.Load())
			}
			if consume(id, actor, digest) == nil {
				t.Fatal("replay")
			}
			var state, hash string
			if e = pool.QueryRow(context.Background(), `SELECT state,ticket_hash FROM sessions WHERE id=$1`, id).Scan(&state, &hash); e != nil || state != "connecting" || hash != "" {
				t.Fatalf("session state %s %s %v", state, hash, e)
			}
		})
	}
}
