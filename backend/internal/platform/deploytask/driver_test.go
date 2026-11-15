package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLiteralCompose(t *testing.T) {
	raw, err := literalCompose([]byte(`{"services":{"server":{"environment":{"PASSWORD":"a${HOME}b$$c"},"command":["echo","$VALUE"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Services map[string]struct {
			Environment map[string]string
			Command     []string
		}
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	server := v.Services["server"]
	if server.Environment["PASSWORD"] != "a$${HOME}b$$$$c" || server.Command[1] != "$$VALUE" {
		t.Fatal(string(raw))
	}
}
func TestHostDriverJournalsAndRollsBack(t *testing.T) {
	e := fixtureExecutor(t, `case "$*" in
 *"config --format json") cat source.json ;;
 *) exit 0 ;;
esac
`)
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	prior := []byte(`{"services":{"server":{"environment":{"KEEP":"literal$"}},"web":{}}}`)
	for _, name := range []string{"source.json", "active.json"} {
		if err := os.WriteFile(filepath.Join(e.Directory, name), prior, 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := &HostDriver{Executor: e, StateDirectory: e.Directory, Check: func(context.Context, Request) error { return nil }}
	if err := d.save(hostState{Owned: map[string]*Mapping{}}); err != nil {
		t.Fatal(err)
	}
	r := Request{Protocol: "mysql", Enabled: true, Bind: "127.0.0.1", Port: 13306}
	if err := d.Apply(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	state, err := d.load()
	if err != nil || state.Owned["mysql"] == nil || string(state.Previous) != string(prior) {
		t.Fatal(state, err)
	}
	if err = d.Rollback(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(e.Files[0])
	if err != nil || string(restored) != string(prior) {
		t.Fatal(string(restored), err)
	}
	state, err = d.load()
	if err != nil || len(state.Owned) != 0 {
		t.Fatal(state, err)
	}
	r.Enabled = false
	if err = d.Rollback(context.Background(), r); err == nil {
		t.Fatal("disable rollback allowed")
	}
}
func TestHostDriverRequiresIntegratedPrecheck(t *testing.T) {
	d := &HostDriver{Executor: ComposeExecutor{Files: []string{"/unused"}}}
	if err := d.Preflight(context.Background(), Request{Protocol: "mysql"}); err == nil {
		t.Fatal("missing precheck accepted")
	}
}

func TestSharedWebsiteFencePreservesMappingOwnership(t *testing.T) {
	e := fixtureExecutor(t, `case "$*" in
 *"config --format json") cat source.json ;;
 *"ps --all --format json"*) echo '[{"Service":"server","State":"running","Health":"healthy","Publishers":[{"URL":"127.0.0.1","TargetPort":18443,"PublishedPort":25443,"Protocol":"tcp"}]},{"Service":"web","State":"running","Health":"healthy"}]' ;;
 *) exit 0 ;;
esac
`)
	raw := []byte(`{"services":{"server":{"ports":[{"target":18443,"published":"25443","host_ip":"127.0.0.1","protocol":"tcp"}]},"web":{}}}`)
	e.Files = []string{filepath.Join(e.Directory, "active.json")}
	for _, name := range []string{"source.json", "active.json"} {
		if err := os.WriteFile(filepath.Join(e.Directory, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := Request{Protocol: "website", Enabled: true, Bind: "127.0.0.1", Port: 25443, SiteID: "00000000-0000-4000-8000-000000000001", SiteVersion: 1}
	owned := map[string]*Mapping{"website": {Bind: r.Bind, Published: r.Port, Target: 18443}}
	d := &HostDriver{Executor: e, StateDirectory: e.Directory, ApplyWebsite: func(context.Context, Request) error { return nil }}
	if err := d.save(hostState{Owned: owned, Previous: raw, PreviousOwned: owned, Request: &r}); err != nil {
		t.Fatal(err)
	}
	if err := d.Fence(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	state, err := d.load()
	if err != nil || state.Owned["website"] == nil || state.Owned["website"].Published != 25443 {
		t.Fatal(state, err)
	}
}
