package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func driveFixture() []byte {
	var d map[string]any
	json.Unmarshal(graphicsFixture(), &d)
	services := d["services"].(map[string]any)
	server := services["server"].(map[string]any)
	g := services["guacd"].(map[string]any)
	mount := map[string]any{"type": "volume", "source": "storageconfig", "target": driveStorageTarget, "read_only": true}
	g["volumes"] = []any{mount}
	g["networks"] = map[string]any{"graphics": nil, "database": nil}
	g["environment"] = map[string]any{"NT_DATABASE_URL_FILE": "/run/secrets/database_url", "NT_STORAGE_PROFILE": driveStorageTarget + "/active.json"}
	g["devices"] = []any{map[string]any{"source": "/dev/fuse", "target": "/dev/fuse"}}
	g["cap_add"] = []string{"SYS_ADMIN", "SETUID", "SETGID", "DAC_OVERRIDE"}
	g["secrets"] = []any{map[string]any{"source": "database_url"}}
	server["volumes"] = []any{map[string]any{"type": "volume", "source": "storageconfig", "target": driveStorageTarget}}
	d["volumes"] = map[string]any{"storageconfig": map[string]any{}}
	d["x-puppeteer-drive"] = map[string]any{"version": 1, "recipe": g}
	raw, _ := json.Marshal(d)
	return raw
}
func TestDrivePlanRemovesPrivilegesAndPreservesStorage(t *testing.T) {
	raw := driveFixture()
	for _, enabled := range []bool{false, true, false, true} {
		p, err := planDrive(raw, Request{Protocol: "rdp-drive", Enabled: enabled}, "/opt/puppeteer/config/puppeteer-deployd", "")
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]json.RawMessage
		var m composeModel
		json.Unmarshal(p.Document, &d)
		json.Unmarshal(p.Document, &m)
		g := m.Services["guacd"]
		if !graphicsEnabled(p.Document) || driveDesired(p.Document) != enabled || d["volumes"] == nil || m.Services["server"]["volumes"] == nil {
			t.Fatal("graphics intent or data volume changed")
		}
		if !enabled {
			for _, key := range []string{"devices", "cap_add", "secrets", "volumes", "environment"} {
				if g[key] != nil {
					t.Fatal("disable retained privilege", key)
				}
			}
			if string(g["user"]) != `"1000:1000"` {
				t.Fatal("guacd remains root")
			}
		}
		if enabled && (!strings.Contains(string(g["volumes"]), driveHelperTarget) || !strings.Contains(string(g["cap_add"]), "SYS_ADMIN")) {
			t.Fatal("missing managed mount")
		}
		if p.Next != nil || g["ports"] != nil {
			t.Fatal("drive publishes a port")
		}
		raw = p.Document
	}
}
func TestDrivePlanRejectsUnsafeRecipes(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(d map[string]any) { delete(d, "x-puppeteer-drive") },
		func(d map[string]any) { d["services"].(map[string]any)["server"].(map[string]any)["volumes"] = []any{} },
		func(d map[string]any) {
			d["volumes"].(map[string]any)["storageconfig"] = map[string]any{"external": true}
		},
		func(d map[string]any) {
			d["x-puppeteer-drive"].(map[string]any)["recipe"].(map[string]any)["devices"] = []any{map[string]any{"source": "/dev/sda", "target": "/dev/fuse"}}
		},
		func(d map[string]any) {
			d["services"].(map[string]any)["guacd"].(map[string]any)["security_opt"] = []string{"apparmor=unconfined"}
		},
		func(d map[string]any) { d["services"].(map[string]any)["guacd"].(map[string]any)["privileged"] = true },
	} {
		var d map[string]any
		json.Unmarshal(driveFixture(), &d)
		change(d)
		raw, _ := json.Marshal(d)
		if _, err := planDrive(raw, Request{Protocol: "rdp-drive", Enabled: true}, "/helper", ""); err == nil {
			t.Fatal("unsafe drive recipe accepted")
		}
	}
	disabled, _ := planGraphics(driveFixture(), Request{Protocol: "graphics", Enabled: false})
	if _, err := planDrive(disabled.Document, Request{Protocol: "rdp-drive", Enabled: true}, "/helper", ""); err == nil {
		t.Fatal("drive enabled with graphics stopped")
	}
	if _, err := planDrive(disabled.Document, Request{Protocol: "rdp-drive", Enabled: false}, "/helper", ""); err != nil {
		t.Fatal("cannot disable stopped drive", err)
	}
}
func TestDriveMissingDeviceDoesNotModifyDeployment(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err == nil {
		t.Skip("requires a host without FUSE device")
	}
	e := fixtureExecutor(t, `case "$*" in *"config --format json") cat active.json;; esac
printf '%s\n' "$*" >> calls
`)
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	raw := driveFixture()
	os.WriteFile(e.Files[0], raw, 0600)
	d := &HostDriver{Executor: e, StateDirectory: e.Directory, Check: func(context.Context, Request) error { return nil }}
	d.save(hostState{Owned: map[string]*Mapping{}})
	if err := d.Apply(context.Background(), Request{Protocol: "rdp-drive", Enabled: true}); err == nil || !strings.Contains(err.Error(), "/dev/fuse") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(e.Files[0])
	calls, _ := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if string(after) != string(raw) || strings.Contains(string(calls), " up ") || strings.Contains(string(calls), "stop ") {
		t.Fatal("failed preflight changed services")
	}
}
func TestDriveProfileMatchesDistributedPolicy(t *testing.T) {
	raw, err := os.ReadFile("../../../../deploy/production/puppeteer-rdp-storage.apparmor")
	if os.IsNotExist(err) {
		t.Skip("backend-only mount")
	}
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(raw), "\r\n", "\n") != driveProfileTemplate {
		t.Fatal("embedded policy differs from published policy")
	}
}
