package application

import (
	"context"
	"next-terminal/backend/internal/modules/connectivity"
	"sync"
	"sync/atomic"
	"testing"
)

func TestR06RegistrationConsumptionAndRevocation(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	r := call(t, srv.URL, "POST", "/api/admin/agent-registration-tokens", map[string]any{"name": "test", "networkScope": map[string]any{"cidrs": []string{"10.0.0.0/24"}, "ports": []int{22}}}, admin, origin)
	expect(t, r, 201)
	obj := object(t, r)
	secret := obj["token"].(string)
	var win atomic.Int32
	var wg sync.WaitGroup
	var enrolled connectivity.Enrolled
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result connectivity.Enrolled
			e := a.db.Within(context.Background(), func(ctx context.Context) error {
				var e error
				result, e = a.connectivity.Enroll(ctx, connectivity.Enrollment{RegistrationToken: secret, Name: "test", OS: "linux", Arch: "amd64", Version: "dev"})
				return e
			})
			if e == nil {
				win.Add(1)
				mu.Lock()
				enrolled = result
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if win.Load() != 1 {
		t.Fatalf("winners %d", win.Load())
	}
	if _, e := a.connectivity.AuthenticateDevice(context.Background(), enrolled.DeviceKey); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/agent-gateways/"+enrolled.AgentID+"/revoke", nil, admin, origin), 204)
	if _, e := a.connectivity.AuthenticateDevice(context.Background(), enrolled.DeviceKey); e == nil {
		t.Fatal("revoked key accepted")
	}
	var plain int
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM agent_gateway_configs WHERE credential_hash=$1`, enrolled.DeviceKey).Scan(&plain)
	if plain != 0 {
		t.Fatal("plaintext persisted")
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/agent-gateways", nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/agent/v1/enroll", map[string]string{"registrationToken": secret, "name": "replay", "os": "linux", "arch": "amd64", "version": "dev"}, nil, ""), 403)
}
