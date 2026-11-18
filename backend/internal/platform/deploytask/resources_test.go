package deploytask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourcePreflightVisibleMemoryFloor(t *testing.T) {
	var doc map[string]any
	_ = json.Unmarshal(resourceFixture(t), &doc)
	delete(doc["services"].(map[string]any), "mp4-renderer")
	for _, tc := range []struct {
		name   string
		memory int64
		cpus   int
		pass   bool
	}{
		{"light", 3758096384, 2, true},
		{"light", 3822956544, 2, true},
		{"light", 3758096383, 2, false},
		{"light", 3221225472, 2, false},
		{"light", 3758096384, 1, false},
		{"standard", 7782 << 20, 4, true},
		{"standard", (7782 << 20) - 1, 4, false},
		{"performance", 15564 << 20, 8, true},
		{"performance", (15564 << 20) - 1, 8, false},
	} {
		t.Run(fmt.Sprintf("%s-%d-%d", tc.name, tc.memory, tc.cpus), func(t *testing.T) {
			e := fixtureExecutor(t, fmt.Sprintf(`case "$1" in
info) echo '{"MemTotal":%d,"NCPU":%d}';;
image) echo v1;;
*) exit 1;;
esac`, tc.memory, tc.cpus))
			err := e.resourcePreflight(context.Background(), jsonValue(doc), Request{Protocol: "resources", Enabled: true, ResourceProfile: tc.name})
			if (err == nil) != tc.pass {
				t.Fatalf("pass=%v, err=%v", tc.pass, err)
			}
			if !tc.pass && !strings.Contains(err.Error(), "Docker 可见内存") {
				t.Fatalf("unexpected failure: %v", err)
			}
		})
	}
}

func resourceFixture(t *testing.T) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(mp4Fixture(), &doc); err != nil {
		t.Fatal(err)
	}
	doc["name"] = "puppeteer-test"
	doc["services"].(map[string]any)["postgres"] = map[string]any{"image": "postgres:18-alpine", "volumes": []any{map[string]any{"type": "volume", "source": "pgdata", "target": "/var/lib/postgresql"}}}
	return jsonValue(doc)
}

func TestResourceProfilesKeepSecretsVolumesAndDisabledComponents(t *testing.T) {
	raw := resourceFixture(t)
	disabled, err := planMP4(raw, Request{Protocol: "mp4", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	raw = disabled.Document
	for _, name := range []string{"light", "standard", "performance", "light"} {
		plan, err := planResources(raw, Request{Protocol: "resources", Enabled: true, ResourceProfile: name})
		if err != nil {
			t.Fatal(err)
		}
		var m composeModel
		_ = json.Unmarshal(plan.Document, &m)
		var env map[string]string
		_ = json.Unmarshal(m.Services["server"]["environment"], &env)
		if env["KEEP"] != "same" || env["NT_RESOURCE_PROFILE"] != name || mp4Enabled(plan.Document) || m.Services["mp4-renderer"]["profiles"] == nil {
			t.Fatal("state or environment changed")
		}
		var before composeModel
		_ = json.Unmarshal(raw, &before)
		for _, service := range []string{"server", "postgres"} {
			if string(before.Services[service]["volumes"]) != string(m.Services[service]["volumes"]) {
				t.Fatal("data volume identity changed")
			}
		}
		if _, err = planMP4(plan.Document, Request{Protocol: "mp4", Enabled: true}); err != nil {
			t.Fatal("disk renderer cannot be enabled", err)
		}
		raw = plan.Document
	}
}

func TestResourceProfileRejectsMountTakeoverAndUnsupportedInputs(t *testing.T) {
	request := Request{Protocol: "resources", Enabled: true, ResourceProfile: "light"}
	plan, err := planResources(resourceFixture(t), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(map[string]any){
		func(d map[string]any) {
			d["volumes"].(map[string]any)["mp4scratch"].(map[string]any)["external"] = true
		},
		func(d map[string]any) {
			d["volumes"].(map[string]any)["mp4scratch"].(map[string]any)["driver_opts"] = map[string]any{"device": "/"}
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["postgres"].(map[string]any)["volumes"] = []any{map[string]any{"source": "mp4scratch", "target": "/data", "type": "volume"}}
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["server"].(map[string]any)["deploy"] = map[string]any{"replicas": 2}
		},
	} {
		var doc map[string]any
		_ = json.Unmarshal(plan.Document, &doc)
		change(doc)
		if _, err = planResources(jsonValue(doc), request); err == nil {
			t.Fatal("unsupported recipe accepted")
		}
	}
	for _, r := range []Request{{Protocol: "resources", ResourceProfile: "light"}, {Protocol: "resources", Enabled: true, ResourceProfile: "unknown"}, {Protocol: "resources", Enabled: true, ResourceProfile: "light", Port: 22}, {Protocol: "mp4", Enabled: true, ResourceProfile: "light"}} {
		if Validate(r) == nil {
			t.Fatal(r)
		}
	}
}

func TestResourceRollbackBeforeMutationDoesNotRestartBusyWorkers(t *testing.T) {
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
echo '{"passed":true}'
`)
	r := Request{Protocol: "resources", Enabled: true, ResourceProfile: "light"}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, resourceFixture(t), "", "  "); err != nil {
		t.Fatal(err)
	}
	prior := pretty.Bytes()
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	if err := os.WriteFile(e.Files[0], prior, 0600); err != nil {
		t.Fatal(err)
	}
	d := &HostDriver{Executor: e, StateDirectory: e.Directory}
	if err := d.save(hostState{Owned: map[string]*Mapping{}, Previous: prior, Request: &r}); err != nil {
		t.Fatal(err)
	}
	if err := d.Rollback(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	calls := string(raw)
	if !strings.Contains(calls, "resource-resume") || strings.Contains(calls, "stop ") || strings.Contains(calls, "force-recreate") {
		t.Fatal(calls)
	}
}

func TestResourceScratchRejectsExistingUnownedVolume(t *testing.T) {
	e := fixtureExecutor(t, `case "$*" in
*"volume ls"*) echo 'test-project_mp4scratch';;
*"volume inspect"*) echo '{"Driver":"local","Options":{},"Labels":{}}';;
esac`)
	e.Project = "test-project"
	if err := e.checkScratchOwnership(context.Background()); err == nil {
		t.Fatal("unowned volume adopted")
	}
}
