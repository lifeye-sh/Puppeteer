package deploytask

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureExecutor(t *testing.T, script string) ComposeExecutor {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return ComposeExecutor{Docker: binary, Directory: dir, Project: "puppeteer", Files: []string{filepath.Join(dir, "managed.json")}}
}
func TestExecutorRestrictedApply(t *testing.T) {
	e := fixtureExecutor(t, `printf '%s\n' "$*" >> calls
exit 0
`)
	if err := e.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(e.Directory, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], "config --quiet") || !strings.HasSuffix(lines[1], "120 server") || !strings.HasSuffix(lines[2], "120 web") {
		t.Fatal(string(raw))
	}
	for _, line := range lines[1:] {
		if !strings.Contains(line, "--no-deps --no-build --pull never") {
			t.Fatal(line)
		}
	}
}
func TestExecutorFailureIsRedacted(t *testing.T) {
	e := fixtureExecutor(t, `echo private-secret >&2
exit 1
`)
	if err := e.Apply(context.Background()); err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal(err)
	}
}
func TestExecutorTimeoutAndValidation(t *testing.T) {
	e := fixtureExecutor(t, "exec sleep 30\n")
	if _, err := e.run(context.Background(), 10*time.Millisecond, "probe", "config"); err == nil {
		t.Fatal("timeout ignored")
	}
	e.Project = "--other"
	if _, err := e.args(); err == nil {
		t.Fatal("invalid project accepted")
	}
	e.Project = "puppeteer"
	e.Files = []string{"relative.yaml"}
	if _, err := e.args(); err == nil {
		t.Fatal("relative file accepted")
	}
}
func TestOutputBound(t *testing.T) {
	b := &limitedOutput{limit: 3}
	n, err := b.Write([]byte("abcdef"))
	if err != nil || n != 6 || b.String() != "abc" || !b.exceeded {
		t.Fatal(b, n, err)
	}
	b.Write([]byte("more"))
	if b.Len() != 3 {
		t.Fatal(b.Len())
	}
}

func TestVerifyPublishedMapping(t *testing.T) {
	raw := []byte(`{"Service":"server","State":"running","Health":"healthy","Publishers":[{"URL":"127.0.0.1","TargetPort":13306,"PublishedPort":13306,"Protocol":"tcp"}]}
{"Service":"web","State":"running","Health":"healthy"}`)
	r := Request{Protocol: "mysql", Enabled: true, Bind: "127.0.0.1", Port: 13306}
	if err := verifyContainers(raw, r); err != nil {
		t.Fatal(err)
	}
	r.Enabled = false
	if err := verifyContainers(raw, r); err == nil {
		t.Fatal("disabled port exposed")
	}
	r.Enabled = true
	r.Bind = "0.0.0.0"
	if err := verifyContainers(raw, r); err == nil {
		t.Fatal("wrong binding accepted")
	}
	if err := verifyContainers([]byte(`[{"Service":"server","State":"running","Health":"healthy"}]`), Request{Protocol: "mysql"}); err == nil {
		t.Fatal("missing web accepted")
	}
	if err := verifyContainers([]byte(`[{"Service":"server","State":"running","Health":"healthy"},{"Service":"web","State":"running","Health":"healthy"}]`), Request{Protocol: "mysql"}); err != nil {
		t.Fatal(err)
	}
}
