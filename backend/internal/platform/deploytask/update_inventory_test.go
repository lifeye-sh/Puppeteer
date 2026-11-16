//go:build linux

package deploytask

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateInventoryDoesNotExposeResolvedSecrets(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "compose-managed.json")
	docker := filepath.Join(dir, "docker")
	model := `{"services":{"server":{"image":"cloners/puppeteer-server:beta0.10","environment":{"PASSWORD":"must-not-appear"}},"mp4-renderer":{"image":"custom/renderer:private","profiles":["puppeteer-mp4-disabled"]}}}`
	os.WriteFile(compose, []byte(model), 0600)
	// Only sanitized fields are requested from inspect; the mock refuses any
	// request for a full config or its environment.
	script := `#!/bin/sh
case "$*" in
 *Environment*|*'json .}}'*) exit 1;;
esac
if [ "$1" = inspect ]; then
 printf '%s\n' '{"runningImage":"cloners/puppeteer-server:beta0.10","imageId":"sha256:fixture","version":"beta0.10","state":"running","health":"healthy"}'
elif [ "$1" = compose ]; then
 case "$*" in *mp4-renderer*) exit 0;; *) printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa;; esac
else exit 1
fi
`
	if e := os.WriteFile(docker, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	d := HostDriver{Executor: ComposeExecutor{Docker: docker, Directory: dir, Project: "fixture", Files: []string{compose}}}
	v := d.UpdateInventory(context.Background())
	raw, _ := json.Marshal(v)
	if !v.Managed || v.RunnerReady || len(v.Components) != 2 || strings.Contains(string(raw), "must-not-appear") {
		t.Fatal(string(raw))
	}
	if v.Components[0].Version != "beta0.10" || !v.Components[1].Disabled || v.Components[1].State != "not_created" {
		t.Fatal(string(raw))
	}
	d.Executor.Files = []string{filepath.Join(dir, "custom.yaml")}
	if d.UpdateInventory(context.Background()).Managed {
		t.Fatal("unmanaged layout accepted")
	}
}
