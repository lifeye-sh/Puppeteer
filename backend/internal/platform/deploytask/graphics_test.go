package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func graphicsFixture() []byte {
	return []byte(`{"x-puppeteer-graphics":{"version":1,"driveEnabled":"true"},"networks":{"graphics":{"internal":true}},"services":{"server":{"image":"server:1","networks":{"graphics":null},"environment":{"NT_GUACD_ADDR":"guacd:4822","NT_GUACD_RELAY_HOST":"server","NT_RDP_DRIVE_ENABLED":"true","KEEP":"unchanged"}},"web":{"image":"web:1"},"guacd":{"image":"graphics:1","labels":{"io.puppeteer.managed.graphics":"v1"},"networks":{"graphics":null},"healthcheck":{"test":["CMD","health"]},"volumes":[{"source":"storageconfig","target":"/storage","type":"volume"}]}}}`)
}

func TestGraphicsPlanPreservesRecipeAndNeverPublishesPorts(t *testing.T) {
	raw := graphicsFixture()
	for _, enabled := range []bool{false, true, false} {
		plan, err := planGraphics(raw, Request{Protocol: "graphics", Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		var doc composeModel
		if err = json.Unmarshal(plan.Document, &doc); err != nil {
			t.Fatal(err)
		}
		var env map[string]string
		json.Unmarshal(doc.Services["server"]["environment"], &env)
		if (env["NT_GUACD_ADDR"] != "") == !enabled || env["KEEP"] != "unchanged" {
			t.Fatal(env)
		}
		if enabled && env["NT_RDP_DRIVE_ENABLED"] != "true" || !enabled && env["NT_RDP_DRIVE_ENABLED"] != "false" {
			t.Fatal(env)
		}
		if doc.Services["guacd"]["volumes"] == nil || doc.Services["guacd"]["ports"] != nil {
			t.Fatal("volumes or exposure changed")
		}
		if (doc.Services["guacd"]["profiles"] != nil) == enabled {
			t.Fatal("disable must survive compose up")
		}
		if plan.Next != nil {
			t.Fatal("internal component acquired host mapping")
		}
		raw = plan.Document
	}
}

func TestGraphicsPlanRejectsUnownedExternalOrPublicComponents(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(d map[string]any) { delete(d, "x-puppeteer-graphics") },
		func(d map[string]any) {
			d["services"].(map[string]any)["guacd"].(map[string]any)["image"] = "registry.example:5000/graphics"
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["guacd"].(map[string]any)["ports"] = []any{map[string]any{"target": 4822, "published": "4822"}}
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["guacd"].(map[string]any)["labels"] = map[string]any{}
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["server"].(map[string]any)["environment"].(map[string]any)["NT_GUACD_ADDR"] = "external:4822"
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["web"].(map[string]any)["depends_on"] = map[string]any{"guacd": map[string]any{}}
		},
		func(d map[string]any) { d["networks"].(map[string]any)["graphics"] = map[string]any{"internal": false} },
	} {
		var doc map[string]any
		json.Unmarshal(graphicsFixture(), &doc)
		change(doc)
		raw, _ := json.Marshal(doc)
		if _, err := planGraphics(raw, Request{Protocol: "graphics", Enabled: true}); err == nil {
			t.Fatal(string(raw))
		}
	}
	for _, r := range []Request{{Protocol: "graphics", Enabled: true, Port: 4822}, {Protocol: "graphics", Bind: "127.0.0.1"}, {Protocol: "graphics", SiteID: "site"}} {
		if Validate(r) == nil {
			t.Fatal("component accepts port/site", r)
		}
	}
}

func TestGraphicsExecutorStopsEvenWhenAdmissionReplacementFails(t *testing.T) {
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
case "$*" in *"120 server") exit 1;; esac
exit 0
`)
	if e.applyGraphics(context.Background(), false) == nil {
		t.Fatal("failure ignored")
	}
	raw, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if !strings.Contains(string(raw), "stop --timeout 15 guacd") {
		t.Fatal("component was left accepting old sessions", string(raw))
	}
	if strings.Contains(string(raw), " down") || strings.Contains(string(raw), "volume") {
		t.Fatal("destructive command")
	}
}

func TestGraphicsRollbackRestoresDisabledProfileAndDriveConfiguration(t *testing.T) {
	e := fixtureExecutor(t, `case "$*" in *"config --format json") cat source.json;; esac
printf '%s\n' "$*" >> calls
`)
	prior, err := planGraphics(graphicsFixture(), Request{Protocol: "graphics", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	for _, name := range []string{"source.json", "active.json"} {
		if err = os.WriteFile(filepath.Join(e.Directory, name), prior.Document, 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := &HostDriver{Executor: e, StateDirectory: e.Directory, Check: func(context.Context, Request) error { return nil }}
	d.save(hostState{Owned: map[string]*Mapping{}})
	r := Request{Protocol: "graphics", Enabled: true}
	if err = d.Apply(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err = d.Rollback(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(e.Files[0])
	if graphicsEnabled(raw) {
		t.Fatal("rollback reopened previously stopped graphics")
	}
	calls, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if !strings.Contains(string(calls), "stop --timeout 15 guacd") {
		t.Fatal(string(calls))
	}
}

func TestGraphicsObservationUsesActualContainers(t *testing.T) {
	for _, tc := range []struct{ raw, state string }{
		{`[]`, "stopped"},
		{`{"Service":"guacd","State":"exited"}`, "stopped"},
		{`[{"Service":"guacd","State":"running","Health":"healthy"}]`, "running"},
		{`[{"Service":"guacd","State":"running","Health":"unhealthy"}]`, "failed"},
		{`[{"Service":"guacd","State":"running","Health":"healthy","Publishers":[{"PublishedPort":4822}]}]`, "failed"},
		{`not-json`, "unknown"},
	} {
		t.Run(tc.state+tc.raw, func(t *testing.T) {
			e := fixtureExecutor(t, "cat containers.json\n")
			e.Files = []string{filepath.Join(e.Directory, "active.json")}
			os.WriteFile(e.Files[0], graphicsFixture(), 0600)
			os.WriteFile(filepath.Join(e.Directory, "containers.json"), []byte(tc.raw), 0600)
			d := &HostDriver{Executor: e}
			got := d.Components(context.Background())["graphics"]
			if !got.Supported || got.State != tc.state || got.ObservedAt.IsZero() {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestGraphicsPreflightDeviceFailureNeverMutates(t *testing.T) {
	var doc map[string]any
	json.Unmarshal(graphicsFixture(), &doc)
	doc["services"].(map[string]any)["guacd"].(map[string]any)["devices"] = []any{map[string]any{"source": "/dev/not-supported", "target": "/dev/fuse"}}
	raw, _ := json.Marshal(doc)
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
case "$*" in *"config --format json") cat active.json;; esac
`)
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	os.WriteFile(e.Files[0], raw, 0600)
	d := &HostDriver{Executor: e, StateDirectory: e.Directory, Check: func(context.Context, Request) error { return nil }}
	d.save(hostState{Owned: map[string]*Mapping{}})
	if d.Apply(context.Background(), Request{Protocol: "graphics", Enabled: true}) == nil {
		t.Fatal("unsupported device accepted")
	}
	after, _ := os.ReadFile(e.Files[0])
	calls, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if string(after) != string(raw) || strings.Contains(string(calls), " up ") || strings.Contains(string(calls), " pull ") {
		t.Fatal("failed preflight modified deployment")
	}
}
