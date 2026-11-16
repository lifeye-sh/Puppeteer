package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeControlAuthenticationAndDrain(t *testing.T) {
	p, a, srv := r02Server(t, false)
	credential := strings.Repeat("t", 48)
	a.config.DeployTokenFile = filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(a.config.DeployTokenFile, []byte(credential), 0600); e != nil {
		t.Fatal(e)
	}
	var actor string
	if e := p.QueryRow(context.Background(), "SELECT id FROM users WHERE username='accept-admin'").Scan(&actor); e != nil {
		t.Fatal(e)
	}
	request := func(operation, token, originHeader, id string) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"actor": id})
		req, _ := http.NewRequest("POST", srv.URL+"/api/internal/deployment-upgrade-"+operation, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if originHeader != "" {
			req.Header.Set("Origin", originHeader)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		var result map[string]any
		_ = json.Unmarshal(body, &result)
		return res.StatusCode, result
	}
	for _, tc := range []struct{ token, origin, id string }{{"bad", "", actor}, {credential, origin, actor}, {credential, "", "00000000-0000-4000-8000-000000000001"}} {
		status, _ := request("drain", tc.token, tc.origin, tc.id)
		if status == 200 {
			t.Fatal("unauthorized control accepted")
		}
	}
	status, out := request("check", credential, "", actor)
	if status != 200 || out["passed"] != true {
		t.Fatal(status, out)
	}
	if _, e := p.Exec(context.Background(), "INSERT INTO jobs(kind,state) VALUES('fixture','running')"); e != nil {
		t.Fatal(e)
	}
	_, out = request("drain", credential, "", actor)
	if out["passed"] != false || a.maintenance.Load() {
		t.Fatal("active job not protected", out)
	}
	if _, e := p.Exec(context.Background(), "DELETE FROM jobs WHERE kind='fixture'"); e != nil {
		t.Fatal(e)
	}
	_, out = request("drain", credential, "", actor)
	if out["passed"] != true || !a.maintenance.Load() {
		t.Fatal("drain failed", out)
	}
	defer func() {
		a.upgradeMu.Lock()
		if a.upgradeRelease != nil {
			a.upgradeRelease()
			a.upgradeRelease = nil
		}
		a.upgradeMu.Unlock()
	}()
	expect(t, call(t, srv.URL, "GET", "/api/branding", nil, nil, origin), 503)
	_, out = request("drain", credential, "", actor)
	if out["passed"] != true {
		t.Fatal("duplicate drain failed", out)
	}
	_, out = request("resume", credential, "", actor)
	if out["passed"] != true || a.maintenance.Load() {
		t.Fatal("resume failed", out)
	}
	if _, e := p.Exec(context.Background(), "UPDATE users SET status='disabled' WHERE id=$1", actor); e != nil {
		t.Fatal(e)
	}
	status, _ = request("drain", credential, "", actor)
	if status == 200 {
		t.Fatal("revoked administrator accepted")
	}
}

func TestUpgradeHoldPreservesQueuedBackup(t *testing.T) {
	p, a, _ := r02Server(t, false)
	ctx := context.Background()
	var id string
	if e := p.QueryRow(ctx, "INSERT INTO jobs(kind,state) VALUES('backup.create','queued') RETURNING id").Scan(&id); e != nil {
		t.Fatal(e)
	}
	a.maintenance.Store(true)
	if e := a.RunBackupOnce(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e := p.QueryRow(ctx, "SELECT state FROM jobs WHERE id=$1", id).Scan(&state); e != nil || state != "queued" {
		t.Fatal("maintenance claimed or failed queued backup", state, e)
	}
}
