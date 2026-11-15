package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func managementFixture(t *testing.T, dir string) []byte {
	t.Helper()
	source := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(source, []byte("events {}\nhttp { server { listen 8443;\n location /api/ {\n  proxy_pass http://server:8089;\n }\n} }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	document := map[string]any{"services": map[string]any{"server": map[string]any{"image": "server-pinned", "environment": map[string]string{"NT_ADDR": ":8089", "KEEP": "value"}}, "web": map[string]any{"image": "web-pinned", "ports": []any{map[string]any{"target": 8443, "published": "443"}}, "volumes": []any{map[string]any{"type": "bind", "source": source, "target": "/etc/nginx/nginx.conf", "read_only": true}}}}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestManagementPlanAndRollback(t *testing.T) {
	e := fixtureExecutor(t, `case "$*" in
 *"config --format json") cat source.json ;;
 *) exit 0 ;;
esac
`)
	raw := managementFixture(t, e.Directory)
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	for _, path := range []string{e.Files[0], filepath.Join(e.Directory, "source.json")} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := &HostDriver{Executor: e, StateDirectory: e.Directory, Check: func(context.Context, Request) error { return nil }}
	if err := d.save(hostState{Owned: map[string]*Mapping{}}); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: "management", Enabled: true, Bind: "0.0.0.0", Port: 9089}
	if err := d.Apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	next, err := os.ReadFile(e.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	var model composeModel
	if json.Unmarshal(next, &model) != nil {
		t.Fatal("invalid config")
	}
	var env map[string]string
	json.Unmarshal(model.Services["server"]["environment"], &env)
	if env["NT_ADDR"] != "0.0.0.0:9089" || env["NT_DEPLOY_MANAGED_HTTP_ADDR"] != env["NT_ADDR"] || env["KEEP"] != "value" {
		t.Fatal(env)
	}
	target, err := nginxSource(next)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(generated), "http://server:9089;") {
		t.Fatal(string(generated), err)
	}
	original, _ := os.ReadFile(filepath.Join(e.Directory, "nginx.conf"))
	if !strings.Contains(string(original), "http://server:8089;") {
		t.Fatal("original Nginx file changed")
	}
	if err = d.Rollback(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(e.Files[0])
	if string(restored) != string(raw) {
		t.Fatal("rollback did not restore both references")
	}
}
func TestManagementRejectsUnsafeRequestsAndCustomUpstream(t *testing.T) {
	for _, r := range []Request{{Protocol: "management"}, {Protocol: "management", Enabled: true, Bind: "127.0.0.1", Port: 8089}} {
		if Validate(r) == nil {
			t.Fatal(r)
		}
	}
	dir := t.TempDir()
	raw := managementFixture(t, dir)
	r := Request{Protocol: "management", Enabled: true, Bind: "0.0.0.0", Port: 9089}
	if _, err := planManagement(raw, []byte("proxy_pass http://custom:8089;"), r, dir); err == nil {
		t.Fatal("custom upstream overwritten")
	}
	source, _ := os.ReadFile(filepath.Join(dir, "nginx.conf"))
	r.Port = 13306
	if _, err := planManagement(raw, source, r, dir); err == nil {
		t.Fatal("reserved protocol conflict ignored")
	}
}

func TestManagementAcceptsCRLF(t *testing.T) {
	dir := t.TempDir()
	raw := managementFixture(t, dir)
	original := []byte("events {}\r\nhttp { server {\r\n proxy_pass http://server:8089;\r\n} }\r\n")
	plan, err := planManagement(raw, original, Request{Protocol: "management", Enabled: true, Bind: "0.0.0.0", Port: 9089}, dir)
	if err != nil || !strings.Contains(string(plan.AuxiliaryContent), "http://server:9089;\r\n") {
		t.Fatal(string(plan.AuxiliaryContent), err)
	}
}
