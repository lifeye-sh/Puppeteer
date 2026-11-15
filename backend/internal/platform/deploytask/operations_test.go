package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessPolicyPreventsLockoutAndInjection(t *testing.T) {
	r := Request{Protocol: "web-access", Enabled: true, AdminIP: "198.51.100.9", AllowCIDRs: "198.51.100.0/24\n2001:db8::/32"}
	policy, e := accessPolicy(r)
	if e != nil || !strings.Contains(string(policy), "allow 127.0.0.1;") || !strings.HasSuffix(string(policy), "deny all;\n") {
		t.Fatal(string(policy), e)
	}
	for _, bad := range []string{"203.0.113.0/24", "198.51.100.9/24", "198.51.100.0/24;allow all;", ""} {
		r.AllowCIDRs = bad
		if Validate(r) == nil {
			t.Fatal("unsafe policy accepted", bad)
		}
	}
	r.Enabled = false
	if Validate(r) != nil {
		t.Fatal("closing access restriction requires an allowlist")
	}
	if Validate(Request{Protocol: "wal", Enabled: true, Port: 5432}) == nil || Validate(Request{Protocol: "mysql", Repository: "https://example.test"}) == nil {
		t.Fatal("cross-protocol options accepted")
	}
}

func TestOperationsWebPlanPreservesMountOptions(t *testing.T) {
	dir := t.TempDir()
	nginx := filepath.Join(dir, "nginx.conf")
	os.WriteFile(nginx, []byte("include /etc/nginx/access-server/*.conf;\ninclude /etc/nginx/optional/*.conf;\nproxy_set_header X-Forwarded-For $remote_addr;\nproxy_set_header X-Real-IP $remote_addr;\nproxy_set_header X-Client-IP $remote_addr;"), 0600)
	source := map[string]any{"services": map[string]any{"web": map[string]any{"volumes": []any{map[string]any{"type": "bind", "source": nginx, "target": "/etc/nginx/nginx.conf", "read_only": true, "bind": map[string]any{"create_host_path": false}}}}, "postgres": map[string]any{"image": "untouched"}}}
	raw, _ := json.Marshal(source)
	d := HostDriver{StateDirectory: dir, Executor: ComposeExecutor{Directory: dir}}
	plan, e := d.opsWebPlan(context.Background(), raw, Request{Protocol: "web-access", Enabled: true, AdminIP: "198.51.100.9", AllowCIDRs: "198.51.100.9/32"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(plan.Document), `"create_host_path":false`) || !strings.Contains(string(plan.Document), `"image":"untouched"`) {
		t.Fatal(string(plan.Document))
	}
	var model composeModel
	json.Unmarshal(plan.Document, &model)
	var mounts []composeMount
	if e = json.Unmarshal(model.Services["web"]["volumes"], &mounts); e != nil || len(mounts) != 2 || mounts[1].Target != "/etc/nginx/access-server/managed.conf" {
		t.Fatal(mounts, e)
	}
	config, _ := os.ReadFile(nginx)
	os.WriteFile(nginx, []byte(strings.Replace(string(config), "X-Forwarded-For $remote_addr", "X-Forwarded-For $proxy_add_x_forwarded_for", 1)), 0600)
	if _, e = d.opsWebPlan(context.Background(), raw, Request{Protocol: "web-access", Enabled: true, AdminIP: "198.51.100.9", AllowCIDRs: "198.51.100.9/32"}); e == nil {
		t.Fatal("client-controlled source chain accepted")
	}
}

func TestWALPlanKeepsDatabaseVolumeAndArchiveOnDisable(t *testing.T) {
	raw := []byte(`{"services":{"postgres":{"image":"postgres:18-alpine@sha256:fixture","environment":{"POSTGRES_USER":"puppeteer","POSTGRES_DB":"puppeteer"},"volumes":[{"type":"volume","source":"pgdata","target":"/var/lib/postgresql","volume":{"nocopy":true}}],"secrets":[{"source":"database_password"}]},"server":{"image":"server"}},"volumes":{"pgdata":{"name":"existing-data"}}}`)
	d := HostDriver{StateDirectory: t.TempDir(), Executor: ComposeExecutor{Project: "fixture"}}
	enabled, e := d.walPlan(context.Background(), raw, Request{Protocol: "wal", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if !walEnabled(enabled.Document) || !strings.Contains(string(enabled.Document), `"nocopy":true`) || !strings.Contains(string(enabled.Document), "existing-data") {
		t.Fatal(string(enabled.Document))
	}
	disabled, e := d.walPlan(context.Background(), enabled.Document, Request{Protocol: "wal"})
	if e != nil {
		t.Fatal(e)
	}
	var model composeModel
	json.Unmarshal(disabled.Document, &model)
	if walEnabled(disabled.Document) || len(model.Services["postgres"]["command"]) != 0 || string(model.Services["postgres"]["image"]) != `"postgres:18-alpine@sha256:fixture"` {
		t.Fatal(string(disabled.Document))
	}
	if !strings.Contains(string(disabled.Document), "fixture_managed_wal") || !strings.Contains(string(disabled.Document), "wal_cipher_pass") {
		t.Fatal("historical archive/key definition deleted")
	}
}
