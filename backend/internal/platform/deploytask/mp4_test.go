package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mp4Fixture() []byte {
	return []byte(`{"x-puppeteer-mp4":{"version":1},"secrets":{"mp4_token":{"file":"/config/mp4_token"}},"networks":{"render":{"internal":true}},"services":{"server":{"image":"server:1","volumes":[{"type":"volume","source":"appdata","target":"/var/lib/puppeteer"}],"networks":{"render":null},"secrets":[{"source":"mp4_token","target":"mp4_token"}],"environment":{"NT_MP4_URL":"http://mp4-renderer:8090","NT_MP4_TOKEN_FILE":"/run/secrets/mp4_token","NT_MP4_DIRECTORY":"/var/lib/puppeteer/mp4","KEEP":"same"},"depends_on":{"mp4-renderer":{"condition":"service_healthy"},"postgres":{"condition":"service_healthy"}}},"web":{"image":"web:1"},"mp4-renderer":{"image":"renderer:1","labels":{"io.puppeteer.managed.mp4":"v1"},"networks":{"render":null},"secrets":[{"source":"mp4_token","target":"mp4_token"}],"environment":{"NT_MP4_TOKEN_FILE":"/run/secrets/mp4_token"},"healthcheck":{"test":["CMD","health"]},"read_only":true,"cap_drop":["ALL"],"security_opt":["no-new-privileges:true"],"tmpfs":["/tmp:rw,size=768m,mode=1777"],"mem_limit":2147483648,"cpus":2,"pids_limit":160}}}`)
}

func TestMP4PlanPreservesCacheAndSecrets(t *testing.T) {
	raw := mp4Fixture()
	for _, enabled := range []bool{false, true, false} {
		plan, err := planMP4(raw, Request{Protocol: "mp4", Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		var m composeModel
		json.Unmarshal(plan.Document, &m)
		var env map[string]string
		json.Unmarshal(m.Services["server"]["environment"], &env)
		if mp4Enabled(plan.Document) != enabled || env["NT_MP4_TOKEN_FILE"] != "/run/secrets/mp4_token" || env["NT_MP4_DIRECTORY"] != "/var/lib/puppeteer/mp4" || env["KEEP"] != "same" {
			t.Fatal(env)
		}
		if (m.Services["mp4-renderer"]["profiles"] == nil) != enabled || m.Services["mp4-renderer"]["ports"] != nil || plan.Next != nil {
			t.Fatal("profile/exposure changed")
		}
		var deps map[string]any
		json.Unmarshal(m.Services["server"]["depends_on"], &deps)
		if deps["mp4-renderer"] != nil || deps["postgres"] == nil {
			t.Fatal(deps)
		}
		raw = plan.Document
	}
}

func TestMP4RejectsUnsupportedRecipes(t *testing.T) {
	for _, change := range []func(map[string]any, map[string]any, map[string]any){
		func(d, s, c map[string]any) { delete(d, "x-puppeteer-mp4") },
		func(d, s, c map[string]any) { delete(s, "volumes") },
		func(d, s, c map[string]any) { s["tmpfs"] = []any{"/var/lib/puppeteer/mp4"} },
		func(d, s, c map[string]any) { c["ports"] = []any{map[string]any{"target": 8090, "published": "8090"}} },
		func(d, s, c map[string]any) { c["privileged"] = true },
		func(d, s, c map[string]any) { c["mem_limit"] = 0 },
		func(d, s, c map[string]any) { c["cpus"] = 0 },
		func(d, s, c map[string]any) { c["pids_limit"] = -1 },
		func(d, s, c map[string]any) { c["command"] = []any{"custom"} },
		func(d, s, c map[string]any) { c["profiles"] = []any{"custom"} },
		func(d, s, c map[string]any) {
			c["secrets"] = []any{map[string]any{"source": "wrong", "target": "mp4_token"}}
		},
		func(d, s, c map[string]any) { s["environment"].(map[string]any)["NT_MP4_URL"] = "https://external" },
		func(d, s, c map[string]any) {
			d["networks"].(map[string]any)["render"] = map[string]any{"internal": true, "external": true}
		},
	} {
		var d map[string]any
		json.Unmarshal(mp4Fixture(), &d)
		services := d["services"].(map[string]any)
		change(d, services["server"].(map[string]any), services["mp4-renderer"].(map[string]any))
		b, _ := json.Marshal(d)
		if _, err := planMP4(b, Request{Protocol: "mp4", Enabled: true}); err == nil {
			t.Fatal(string(b))
		}
	}
	for _, r := range []Request{{Protocol: "mp4", Port: 8090}, {Protocol: "mp4", Bind: "127.0.0.1"}, {Protocol: "mp4", SiteID: "site"}} {
		if Validate(r) == nil {
			t.Fatal(r)
		}
	}
}

func TestMP4ExecutorOrdersQueueAndSampleBeforeWorker(t *testing.T) {
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
case "$*" in *"ps --all"*) echo '[{"Service":"server","State":"exited"}]';; esac
exit 0
`)
	if err := e.applyMP4(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	calls := string(b)
	last := -1
	for _, part := range []string{"stop --timeout 30 server", "stop --timeout 15 mp4-renderer", "ps --all", "deployment-mp4-queue", "120 mp4-renderer", "deployment-mp4-sample", "120 server", "120 web"} {
		pos := strings.Index(calls, part)
		if pos <= last {
			t.Fatalf("%s out of order: %s", part, calls)
		}
		last = pos
	}
}

func TestMP4ExecutorDoesNotStealLiveLease(t *testing.T) {
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
case "$*" in *"ps --all"*) echo '[{"Service":"server","State":"running"}]';; esac
exit 0
`)
	if e.applyMP4(context.Background(), false) == nil {
		t.Fatal("accepted live worker")
	}
	b, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if strings.Contains(string(b), "deployment-mp4-queue") {
		t.Fatal("reset live lease")
	}
}

func TestMP4AcceptsNormalizedCompose(t *testing.T) {
	var d map[string]any
	json.Unmarshal(mp4Fixture(), &d)
	c := d["services"].(map[string]any)["mp4-renderer"].(map[string]any)
	c["mem_limit"] = "2147483648"
	c["command"] = nil
	c["entrypoint"] = nil
	b, _ := json.Marshal(d)
	if _, err := planMP4(b, Request{Protocol: "mp4", Enabled: true}); err != nil {
		t.Fatal(err)
	}
}
