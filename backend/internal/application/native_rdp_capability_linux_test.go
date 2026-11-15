//go:build linux

package application

import (
	"context"
	"encoding/json"
	"net"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/listener"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func nativeCapabilityFixture(t *testing.T, a *App) system.ConfigurationValues {
	t.Helper()
	root := t.TempDir()
	_, cert, key, _ := r14ProbeCertificate(t, root, "front", false)
	worker, encoder := filepath.Join(root, "worker"), filepath.Join(root, "encoder")
	if e := os.WriteFile(worker, []byte("#!/bin/sh\nprintf '%s\\n' '{\"version\":1,\"recordedView\":true}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(encoder, []byte("#!/bin/sh\nprintf '%s\\n' ' V....D libx264 H.264 encoder'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	a.ConfigureNativeRDP(NativeRDPOptions{Address: "127.0.0.1:0", Worker: worker, FFmpeg: encoder, Certificate: cert, PrivateKey: key})
	return system.ConfigurationValues{"native-rdp-enabled": true, "native-rdp-addr": "", "native-rdp-public-address": ""}
}

func TestNativeRDPCapabilityInspectsFailuresWithoutLeakingPaths(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*App, system.ConfigurationValues)
	}{
		{"missing deployment", "rdp_deployment", func(a *App, v system.ConfigurationValues) { a.managedRDP = nil }},
		{"nonloopback", "rdp_listener_scope", func(a *App, v system.ConfigurationValues) { v["native-rdp-addr"] = "0.0.0.0:3389" }},
		{"worker missing", "rdp_worker", func(a *App, v system.ConfigurationValues) { a.managedRDP.Worker = "/missing-sensitive-worker" }},
		{"encoder mode", "rdp_ffmpeg", func(a *App, v system.ConfigurationValues) { os.Chmod(a.managedRDP.FFmpeg, 0600) }},
		{"key mismatch", "rdp_tls_identity", func(a *App, v system.ConfigurationValues) {
			os.WriteFile(a.managedRDP.PrivateKey, []byte("sensitive-invalid-key"), 0600)
		}},
		{"pipe key", "rdp_tls_identity", func(a *App, v system.ConfigurationValues) {
			path := filepath.Join(t.TempDir(), "pipe")
			if e := syscall.Mkfifo(path, 0600); e != nil {
				t.Fatal(e)
			}
			a.managedRDP.PrivateKey = path
		}},
		{"wrong san", "rdp_tls_endpoint", func(a *App, v system.ConfigurationValues) { v["native-rdp-public-address"] = "other.invalid:3389" }},
		{"expired", "rdp_tls_validity", func(a *App, v system.ConfigurationValues) {
			_, cert, key, _ := r14ProbeCertificate(t, t.TempDir(), "expired", true)
			a.managedRDP.Certificate = cert
			a.managedRDP.PrivateKey = key
		}},
		{"maintenance", "rdp_runtime", func(a *App, v system.ConfigurationValues) { a.maintenance.Store(true) }},
		{"capacity full", "rdp_capacity", func(a *App, v system.ConfigurationValues) {
			a.managedRDPSlots <- struct{}{}
			a.managedRDPSlots <- struct{}{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{}
			v := nativeCapabilityFixture(t, a)
			before := a.inspectNativeRDP(v)
			if !before.StaticReady || !before.Supported {
				t.Fatal(before)
			}
			tc.change(a, v)
			r := a.inspectNativeRDP(v)
			if r.StaticReady {
				t.Fatal(r)
			}
			found := false
			for _, c := range r.Checks {
				if c.Code == tc.code && c.State == "blocked" {
					found = true
				}
			}
			if !found {
				t.Fatal(r)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "sensitive-") || strings.Contains(string(b), "/tmp/") {
				t.Fatal("deployment details leaked", string(b))
			}
		})
	}
	a := &App{}
	v := nativeCapabilityFixture(t, a)
	a.managedRDP.ContainerProfile = true
	v["native-rdp-addr"] = "0.0.0.0:13389"
	v["native-rdp-public-address"] = "front:13389"
	r := a.inspectNativeRDP(v)
	if !r.StaticReady || r.Profile != "windows-local" {
		t.Fatal(r)
	}
}

func TestNativeRDPExecutableCapabilityProbes(t *testing.T) {
	a := &App{}
	nativeCapabilityFixture(t, a)
	for _, check := range nativeRDPExecutableChecks(context.Background(), *a.managedRDP) {
		if check.State != "ready" {
			t.Fatal(check)
		}
	}
	if e := os.WriteFile(a.managedRDP.Worker, []byte("#!/bin/sh\necho sensitive-error >&2\nexit 2\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(a.managedRDP.FFmpeg, []byte("#!/bin/sh\necho 'other encoder'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, check := range nativeRDPExecutableChecks(context.Background(), *a.managedRDP) {
		if check.State != "blocked" || strings.Contains(check.Summary, "sensitive-error") {
			t.Fatal(check)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, check := range nativeRDPExecutableChecks(ctx, *a.managedRDP) {
		if check.State != "blocked" {
			t.Fatal("cancel ignored")
		}
	}
}

func TestNativeRDPCapabilityRealBinaries(t *testing.T) {
	worker := os.Getenv("NT_RDP_WORKER_BINARY")
	encoder := os.Getenv("NT_RDP_FFMPEG_BINARY")
	if worker == "" || encoder == "" {
		t.Skip("dedicated native RDP image required")
	}
	for _, check := range nativeRDPExecutableChecks(context.Background(), NativeRDPOptions{Worker: worker, FFmpeg: encoder}) {
		if check.State != "ready" {
			t.Fatal(check)
		}
	}
}

func TestNativeRDPConfigurationPreflightIndependentOfHost(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	v := nativeCapabilityFixture(t, a)
	a.listenerManager = listener.New(context.Background(), 4)
	defer func() { a.listenerManager.Close(); a.listenerManager.Wait() }()
	statusRow := func() configurationFeature {
		t.Helper()
		r := call(t, srv.URL, "GET", "/api/settings/operations", nil, admin, origin)
		expect(t, r, 200)
		var body struct{ Items []configurationFeature }
		if e := json.Unmarshal(r.body, &body); e != nil {
			t.Fatal(e)
		}
		for _, row := range body.Items {
			if row.ID == "native-rdp" {
				return row
			}
		}
		t.Fatal("missing native RDP")
		return configurationFeature{}
	}
	row := statusRow()
	if !row.CheckSupported || row.Capability == nil || row.Service.State != "stopped" {
		t.Fatal(row)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ := settingsRequest(t, srv.URL, "POST", "/api/settings/operations/native-rdp/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	task := configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	if task.State != "succeeded" || task.Generation != nil || task.Evidence == "" {
		t.Fatal(task)
	}
	if row = statusRow(); row.Dependencies.State != "ready" || len(a.listenerManager.States()) != 0 {
		t.Fatal("preflight published listener or required host", row)
	}
	// A real managed listener is named rdp, while the feature is native-rdp.
	spec, e := a.managedRDPSpec(v)
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := a.listenerManager.Prepare(context.Background(), []listener.Spec{*spec})
	if e != nil {
		t.Fatal(e)
	}
	if e = prepared.Commit(); e != nil {
		t.Fatal(e)
	}
	if row = statusRow(); row.Service.State != "degraded" {
		t.Fatal("listener missing or unconfirmed revision claimed ready", row.Service)
	}
	// Modifying a previously checked executable invalidates old evidence.
	if e = os.WriteFile(a.managedRDP.Worker, []byte("#!/bin/sh\nexit 2\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if row = statusRow(); row.Dependencies.State != "stale" {
		t.Fatal(row.Dependencies)
	}
	// An occupied candidate port fails without changing current listeners.
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	a.managedRDP.Address = occupied.Addr().String()
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/operations/native-rdp/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	task = configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	if task.State != "failed" {
		t.Fatal(task)
	}
	found := false
	for _, c := range task.Checks {
		if c.Code == "rdp_port" && c.State == "blocked" {
			found = true
		}
	}
	if !found || len(a.listenerManager.States()) != 1 {
		t.Fatal(task)
	}
	// Unsupported ordinary production deployments still provide an actionable report.
	a.managedRDP = nil
	r, _ = settingsRequest(t, srv.URL, "POST", "/api/settings/operations/native-rdp/preflight", tag, map[string]any{}, admin)
	expect(t, r, 202)
	task = configurationTaskResult(t, srv, admin, object(t, r)["id"].(string))
	if task.State != "failed" {
		t.Fatal(task)
	}
}
