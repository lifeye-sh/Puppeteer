package deploytask

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"puppeteer/backend/internal/agentupdate"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in Linux runner has a Docker CLI/socket and a disposable volume mounted
// at its actual host mountpoint. No application container receives the socket.
func TestOperationsRealDocker(t *testing.T) {
	root := os.Getenv("NT_OPERATIONS_DOCKER_ROOT")
	project := os.Getenv("NT_OPERATIONS_DOCKER_PROJECT")
	runner := os.Getenv("NT_OPERATIONS_DOCKER_RUNNER")
	if root == "" {
		t.Skip("requires disposable Docker operations runner")
	}
	if !strings.HasPrefix(root, "/var/lib/docker/volumes/") || !strings.HasPrefix(project, "puppeteer-p2-") {
		t.Fatal("refusing non-fixture deployment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	docker, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal(e)
	}
	executor := ComposeExecutor{Docker: docker, Directory: root, Project: project, Files: []string{filepath.Join(root, "compose.json")}}
	state := filepath.Join(root, "state")
	os.Mkdir(state, 0700)
	os.Mkdir(filepath.Join(root, "html"), 0755)
	os.Mkdir(filepath.Join(root, "tls"), 0755)
	write := func(name string, raw []byte, mode os.FileMode) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, name), raw, mode); e != nil {
			t.Fatal(e)
		}
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost", "web"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	write("tls/server.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644)
	write("tls/server.key", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0644)
	write("html/index.html", []byte("operations fixture"), 0644)
	write("nginx.conf", []byte(`events {} http { server { listen 8443 ssl; ssl_certificate /run/tls/server.crt; ssl_certificate_key /run/tls/server.key; root /usr/share/nginx/html;
 proxy_set_header X-Forwarded-For $remote_addr;
 proxy_set_header X-Real-IP $remote_addr;
 proxy_set_header X-Client-IP $remote_addr;
 include /etc/nginx/access-server/*.conf;
 include /etc/nginx/optional/*.conf;
 location = /healthz { return 200 'ok'; }
 location / { try_files $uri /index.html; }
 } }`), 0644)
	pg := map[string]any{"image": "postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5", "environment": map[string]string{"POSTGRES_USER": "puppeteer", "POSTGRES_DB": "puppeteer", "POSTGRES_PASSWORD": "fixture-only"}, "volumes": []string{"pgdata:/var/lib/postgresql"}, "healthcheck": map[string]any{"test": []string{"CMD", "pg_isready", "-h", "127.0.0.1", "-U", "puppeteer", "-d", "puppeteer"}, "interval": "1s", "retries": 60}}
	web := map[string]any{"image": "nginx:alpine", "volumes": []string{filepath.Join(root, "nginx.conf") + ":/etc/nginx/nginx.conf:ro", filepath.Join(root, "tls") + ":/run/tls:ro", filepath.Join(root, "html") + ":/usr/share/nginx/html:ro"}, "healthcheck": map[string]any{"test": []string{"CMD-SHELL", "wget -q --no-check-certificate -O /dev/null https://127.0.0.1:8443/healthz"}, "interval": "1s", "retries": 30}}
	model := map[string]any{"name": project, "services": map[string]any{"postgres": pg, "web": web, "server": map[string]any{"image": "nginx:alpine"}}, "volumes": map[string]any{"pgdata": map[string]string{"name": project + "_data"}}, "x-puppeteer-operations": operationsEnrollment{Version: 1, WAL: true}}
	raw, _ := json.Marshal(model)
	write("compose.json", raw, 0600)
	defer executor.run(context.Background(), time.Minute, "fixture teardown", "down", "--volumes", "--remove-orphans")
	if _, e = executor.run(ctx, 2*time.Minute, "fixture startup", "up", "-d", "--wait", "--wait-timeout", "90"); e != nil {
		t.Fatal(e)
	}
	if _, e = executor.run(ctx, 10*time.Second, "fixture schema", "exec", "-T", "--user", "postgres", "postgres", "psql", "-U", "puppeteer", "-d", "puppeteer", "-c", "CREATE TABLE instance_state(singleton integer primary key); INSERT INTO instance_state VALUES(1);"); e != nil {
		t.Fatal(e)
	}
	if _, e = executor.execute(ctx, 10*time.Second, "attach test runner", "network", "connect", project+"_default", runner); e != nil {
		t.Fatal(e)
	}
	defer executor.execute(context.Background(), 10*time.Second, "detach runner", "network", "disconnect", project+"_default", runner)
	b, e := executor.execute(ctx, 10*time.Second, "inspect runner", "inspect", "--format", "{{(index .NetworkSettings.Networks \""+project+"_default\").IPAddress}}", runner)
	if e != nil {
		t.Fatal(e)
	}
	ip := strings.TrimSpace(string(b))
	d := &HostDriver{Executor: executor, StateDirectory: state, Check: func(context.Context, Request) error { return nil }, WALGuard: func(context.Context) error { return nil }}
	if e = d.save(hostState{Owned: map[string]*Mapping{}}); e != nil {
		t.Fatal(e)
	}
	engine, e := Open(ctx, state, d)
	if e != nil {
		t.Fatal(e)
	}
	d.Progress = func(stage string) error { t.Log("stage", stage); return engine.Report(stage) }
	run := func(r Request) {
		t.Helper()
		r.Generation, _ = engine.View()
		if e := engine.Preflight(ctx, r); e != nil {
			t.Fatal(r.Protocol, "preflight", e)
		}
		if _, e := engine.Start(ctx, r); e != nil {
			t.Fatal(e)
		}
		if e := engine.Wait(ctx); e != nil {
			t.Fatal(e)
		}
		_, task := engine.View()
		if task.State != "succeeded" {
			if b, e := os.ReadFile(filepath.Join(state, "wal-last-error.log")); e == nil {
				t.Log("WAL fixture diagnostic", string(b))
			}
			t.Fatal(task)
		}
		t.Log(r.Protocol, r.Enabled, "verified")
	}
	if os.Getenv("NT_OPERATIONS_ONLY_WAL") != "1" {
		original, _ := os.ReadFile(executor.Files[0])
		bad := Request{Protocol: "web-access", Enabled: true, AdminIP: ip, AllowCIDRs: "203.0.113.0/24"}
		if engine.Preflight(ctx, bad) == nil {
			t.Fatal("lockout accepted")
		}
		after, _ := os.ReadFile(executor.Files[0])
		if string(original) != string(after) {
			t.Fatal("failed preflight changed deployment")
		}
		run(Request{Protocol: "web-access", Enabled: true, AdminIP: ip, AllowCIDRs: ip + "/32"})
		// This request comes from the allowlisted runner, not loopback or a forged header.
		command := exec.CommandContext(ctx, "curl", "-ksS", "--noproxy", "*", "-o", "/dev/null", "-w", "%{http_code}", "https://web:8443/index.html")
		b, e = command.Output()
		if e != nil || string(b) != "200" {
			t.Fatal("administrator blocked", string(b), e)
		}
		if _, e = executor.run(ctx, 10*time.Second, "check denied client", "exec", "-T", "server", "sh", "-c", "if wget -q --no-check-certificate -T 3 -O /dev/null https://web:8443/index.html; then exit 1; fi"); e != nil {
			t.Fatal("disallowed client reached web", e)
		}
		run(Request{Protocol: "web-access", Enabled: false})
		// Interrupted application is rolled back without asking the API again.
		req := Request{Protocol: "web-access", Enabled: true, AdminIP: ip, AllowCIDRs: ip + "/32"}
		req.Generation, _ = engine.View()
		d.Progress = nil
		if e = d.specializedApply(ctx, req); e != nil {
			t.Fatal(e)
		}
		interrupted, _ := json.Marshal(snapshot{Generation: req.Generation + 1, Task: &Task{ID: "interrupted-fixture", Request: req, State: "running", Stage: "apply"}})
		if e = atomicPrivate(filepath.Join(state, "task.json"), interrupted); e != nil {
			t.Fatal(e)
		}
		d.Check = func(context.Context, Request) error { return errors.New("fixture API offline") }
		engine, e = Open(ctx, state, d)
		if e != nil {
			t.Fatal(e)
		}
		_, recovered := engine.View()
		if recovered.State != "failed" || !strings.Contains(recovered.Error, "recovered") {
			t.Fatal(recovered)
		}
		if _, e = executor.run(ctx, 10*time.Second, "check rollback restored access", "exec", "-T", "server", "wget", "-q", "--no-check-certificate", "-O", "/dev/null", "https://web:8443/index.html"); e != nil {
			t.Fatal(e)
		}
		d.Check = func(context.Context, Request) error { return nil }
		run(Request{Protocol: "web-access", Enabled: true, AdminIP: ip, AllowCIDRs: ip + "/32"})
		// Fail after writing a disable plan and make the API unreachable. The
		// recovery path must still finish opening the management entry.
		d.Progress = func(stage string) error {
			if stage == "apply_nginx" {
				d.Check = func(context.Context, Request) error { return errors.New("fixture API offline") }
				return errors.New("injected restart failure")
			}
			return engine.Report(stage)
		}
		req = Request{Protocol: "web-access"}
		req.Generation, _ = engine.View()
		if _, e = engine.Start(ctx, req); e != nil {
			t.Fatal(e)
		}
		if e = engine.Wait(ctx); e != nil {
			t.Fatal(e)
		}
		_, recovered = engine.View()
		if recovered.State != "failed" || strings.Contains(recovered.Error, "fence failed") {
			t.Fatal(recovered)
		}
		if _, e = executor.run(ctx, 10*time.Second, "check disable recovered access", "exec", "-T", "server", "wget", "-q", "--no-check-certificate", "-O", "/dev/null", "https://web:8443/index.html"); e != nil {
			t.Fatal(e)
		}
		d.Check = func(context.Context, Request) error { return nil }
		d.Progress = func(stage string) error { t.Log("stage", stage); return engine.Report(stage) }
		repo, keys := filepath.Join(root, "source"), filepath.Join(root, "private-keys")
		if e = agentupdate.InitRepository(repo, keys); e != nil {
			t.Fatal(e)
		}
		binary := filepath.Join(root, "fixture-agent")
		os.WriteFile(binary, []byte("signed fixture binary"), 0700)
		if e = agentupdate.Publish(repo, keys, binary, agentupdate.Release{Sequence: 1, Version: "fixture", OS: "linux", Arch: "amd64"}, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
		vendor := httptest.NewTLSServer(http.FileServer(http.Dir(repo)))
		defer vendor.Close()
		previous := http.DefaultTransport
		http.DefaultTransport = vendor.Client().Transport
		defer func() { http.DefaultTransport = previous }()
		trust, _ := os.ReadFile(filepath.Join(repo, "1.root.json"))
		run(Request{Protocol: "agent-repository", Enabled: true, Repository: vendor.URL, TrustRoot: string(trust)})
		if _, e = executor.run(ctx, 10*time.Second, "check signed repository HTTP", "exec", "-T", "web", "wget", "-q", "--no-check-certificate", "-O", "/dev/null", "https://127.0.0.1:8443/agent-updates/timestamp.json"); e != nil {
			t.Fatal(e)
		}
		run(Request{Protocol: "agent-repository", Enabled: false})
	}
	run(Request{Protocol: "wal", Enabled: true})
	view := d.Operations(ctx)["wal"]
	if view.Service != "recoverable" || view.Schedule == nil {
		t.Fatal(view)
	}
	// Periodic jobs use the same gate and pause while a restored instance is frozen.
	schedule := *view.Schedule
	schedule.Backup = time.Now().Add(-25 * time.Hour)
	if e = d.saveWALSchedule(schedule); e != nil {
		t.Fatal(e)
	}
	d.WALGuard = func(context.Context) error { return errors.New("fixture restore quarantine") }
	if e = engine.Maintain(ctx, d.maintainWAL); e != nil {
		t.Fatal(e)
	}
	if d.Operations(ctx)["wal"].Service != "failed" {
		t.Fatal("quarantine ignored")
	}
	d.WALGuard = func(context.Context) error { return nil }
	if e = engine.Maintain(ctx, d.maintainWAL); e != nil {
		t.Fatal(e)
	}
	view = d.Operations(ctx)["wal"]
	if view.Schedule == nil || time.Since(view.Schedule.Backup) > time.Minute || view.Schedule.Error != "" {
		t.Fatal(view)
	}
	// Simulate a killed daemon leaving an isolated restore volume/container.
	orphan := project + "-wal-drill-0123456789abcdef"
	if _, e = executor.execute(ctx, 15*time.Second, "fixture orphan volume", "volume", "create", "--label", "io.puppeteer.wal-drill="+project, orphan+"-data"); e != nil {
		t.Fatal(e)
	}
	if _, e = executor.execute(ctx, 15*time.Second, "fixture orphan container", "create", "--name", orphan, "--label", "io.puppeteer.wal-drill="+project, "--mount", "type=volume,source="+orphan+"-data,target=/restore", walImage()); e != nil {
		t.Fatal(e)
	}
	if e = d.RecoverWALDrills(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = executor.execute(ctx, 10*time.Second, "check source archive preserved", "volume", "inspect", project+"_managed_wal"); e != nil {
		t.Fatal(e)
	}
	if _, e = executor.execute(ctx, 10*time.Second, "check orphan removed", "volume", "inspect", orphan+"-data"); e == nil {
		t.Fatal("orphan volume remains")
	}
	saved, _ := os.ReadFile(filepath.Join(state, "wal_cipher_pass"))
	run(Request{Protocol: "wal", Enabled: false})
	retained, _ := os.ReadFile(filepath.Join(state, "wal_cipher_pass"))
	if string(saved) != string(retained) {
		t.Fatal("disable removed/rotated archive key")
	}
	if _, e = executor.run(ctx, 10*time.Second, "check source retained", "exec", "-T", "--user", "postgres", "postgres", "psql", "-U", "puppeteer", "-d", "puppeteer", "-Atc", "SELECT count(*) FROM instance_state"); e != nil {
		t.Fatal(e)
	}
}
