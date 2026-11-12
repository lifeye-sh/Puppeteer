package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/modules/connectivity"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real SSH transport; remote systemd execution is simulated. The uploaded
// config is used by a real Agent connection back to the test server.
func installerTarget(t *testing.T, fail bool, release <-chan struct{}) (string, int, <-chan agent.Config) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "tester" && string(p) == "remote-only-secret-R03" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	received := make(chan agent.Config, 1)
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); ln.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := ln.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				stop := context.AfterFunc(ctx, func() { raw.Close() })
				defer stop()
				conn, channels, reqs, e := ssh.NewServerConn(raw, cfg)
				if e != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for next := range channels {
					if next.ChannelType() != "session" {
						next.Reject(ssh.UnknownChannelType, "")
						continue
					}
					ch, requests, e := next.Accept()
					if e != nil {
						return
					}
					for req := range requests {
						if req.Type != "exec" {
							req.Reply(false, nil)
							continue
						}
						var cmd struct{ Command string }
						_ = ssh.Unmarshal(req.Payload, &cmd)
						req.Reply(true, nil)
						code := uint32(0)
						if cmd.Command == installPreflight {
							select {
							case <-release:
							case <-ctx.Done():
								ch.Close()
								return
							}
							_, _ = io.WriteString(ch, "root")
						} else {
							tr := tar.NewReader(ch)
							var config agent.Config
							files := map[string]bool{}
							for {
								h, err := tr.Next()
								if err == io.EOF {
									break
								}
								if err != nil {
									code = 1
									break
								}
								files[h.Name] = true
								b, err := io.ReadAll(io.LimitReader(tr, 128*1024*1024))
								if err != nil {
									code = 1
									break
								}
								if h.Name == "agent.json" {
									if h.Mode != 0600 || json.Unmarshal(b, &config) != nil {
										code = 1
									}
								}
							}
							if !files["nt-agent"] || !files["agent.json"] || !files["nt-agent.service"] {
								code = 1
							}
							received <- config
							if fail {
								code = 1
							} else if code == 0 {
								go func() { _ = agent.RunConnection(ctx, config, func(int64) {}) }()
							}
						}
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
						ch.Close()
						break
					}
				}
			}()
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port, received
}

func TestR06AssetInstaller(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failed_install_revokes_identity"
		}
		t.Run(name, func(t *testing.T) {
			_, a, srv := r02Server(t, false)
			admin := loginAs(t, srv, "accept-admin")
			a.config.AgentBinaryPath = filepath.Join(t.TempDir(), "agent")
			if e := os.WriteFile(a.config.AgentBinaryPath, []byte("test-binary"), 0700); e != nil {
				t.Fatal(e)
			}
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			host, port, received := installerTarget(t, fail, release)
			id := r04Asset(t, srv, admin, host, port, true)
			in := agentInstallInput{AssetID: id, Name: "test '; no shell expansion", Endpoint: srv.URL, Development: true, Scope: connectivity.NetworkScope{CIDRs: []string{"127.0.0.1/32"}, Ports: []int{22}}}
			expect(t, call(t, srv.URL, "POST", "/api/admin/agent-installations", in, nil, origin), 401)
			in.Development = false
			expect(t, call(t, srv.URL, "POST", "/api/admin/agent-installations", in, admin, origin), 422)
			in.Development = true
			expect(t, call(t, srv.URL, "POST", "/api/admin/agent-installations", in, admin, "https://untrusted.invalid"), 403)
			r := call(t, srv.URL, "POST", "/api/admin/agent-installations", in, admin, origin)
			expect(t, r, 202)
			job := object(t, r)["id"].(string)
			expect(t, call(t, srv.URL, "POST", "/api/admin/agent-installations", in, admin, origin), 409)
			unblock()
			var uploaded agent.Config
			select {
			case uploaded = <-received:
			case <-time.After(10 * time.Second):
				t.Fatal("no uploaded config")
			}
			if uploaded.Endpoint != srv.URL || len(uploaded.DeviceKey) != 64 {
				t.Fatal("invalid private config")
			}
			deadline := time.Now().Add(12 * time.Second)
			var state string
			for time.Now().Before(deadline) {
				if e := a.db.Row(context.Background(), `SELECT state FROM agent_installations WHERE id=$1`, job).Scan(&state); e != nil {
					t.Fatal(e)
				}
				if state != "running" {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			want := "succeeded"
			if fail {
				want = "failed"
			}
			if state != want {
				t.Fatalf("state %s, want %s", state, want)
			}
			if fail {
				if _, e := a.connectivity.AuthenticateDevice(context.Background(), uploaded.DeviceKey); e == nil {
					t.Fatal("failed installation device still authorized")
				}
			}
			result := call(t, srv.URL, "GET", "/api/admin/agent-installations", nil, admin, "")
			expect(t, result, 200)
			if bytes.Contains(result.body, []byte(uploaded.DeviceKey)) {
				t.Fatal("device key disclosed")
			}
			var count int
			if e := a.db.Row(context.Background(), `SELECT count(*) FROM secret_access_events WHERE request_id=$1 AND purpose='agent_installation'`, job).Scan(&count); e != nil || count != 1 {
				t.Fatalf("credential audit %d %v", count, e)
			}
		})
	}
}

func TestInstallScriptsAndArchive(t *testing.T) {
	for _, script := range []string{installPreflight, installScript} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if raw, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("shell syntax: %v %s", e, raw)
		}
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if e := writeInstallTar(tw, strings.NewReader("binary"), 6, []byte(`{"secret":"value"}`)); e != nil {
		t.Fatal(e)
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(&archive)
	count := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(h.Name, "/") {
			t.Fatal("unsafe archive name")
		}
		if h.Name == "agent.json" && h.Mode != 0600 {
			t.Fatal("config permissions")
		}
		count++
	}
	if count != 3 {
		t.Fatal(count)
	}
}
