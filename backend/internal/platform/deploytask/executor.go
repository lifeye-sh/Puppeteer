package deploytask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// ComposeExecutor is host-only. Its paths and project come from installer-owned
// configuration, never an API request. The caller holds the deployment lock.
type ComposeExecutor struct {
	Docker    string
	Directory string
	Project   string
	Files     []string
	EnvFile   string
}

var projectName = regexp.MustCompile(`^[a-z][a-z0-9-]{2,40}$`)

func (e ComposeExecutor) args() ([]string, error) {
	if !filepath.IsAbs(e.Docker) || !filepath.IsAbs(e.Directory) || !projectName.MatchString(e.Project) || len(e.Files) == 0 {
		return nil, errors.New("invalid enrolled Compose executor")
	}
	args := []string{"compose", "--project-directory", e.Directory, "--project-name", e.Project}
	if e.EnvFile != "" {
		if !filepath.IsAbs(e.EnvFile) {
			return nil, errors.New("environment file must be absolute")
		}
		args = append(args, "--env-file", e.EnvFile)
	}
	for _, file := range e.Files {
		if !filepath.IsAbs(file) {
			return nil, errors.New("Compose file must be absolute")
		}
		args = append(args, "-f", file)
	}
	return args, nil
}

type limitedOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining < len(p) {
		b.exceeded = true
		p = p[:remaining]
	}
	b.Buffer.Write(p)
	return n, nil
}

// run never returns Docker stderr or exec errors: these can include environment
// secrets or resolved config. Output is bounded even when the subprocess fails.
func (e ComposeExecutor) run(ctx context.Context, timeout time.Duration, operation string, tail ...string) ([]byte, error) {
	args, err := e.args()
	if err != nil {
		return nil, err
	}
	return e.execute(ctx, timeout, operation, append(args, tail...)...)
}

func (e ComposeExecutor) execute(ctx context.Context, timeout time.Duration, operation string, args ...string) ([]byte, error) {
	if _, err := e.args(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, e.Docker, args...)
	command.Dir = e.Directory
	command.WaitDelay = 2 * time.Second
	out := &limitedOutput{limit: 8 << 20}
	command.Stdout = out
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Compose %s cancelled or timed out", operation)
		}
		return nil, fmt.Errorf("Compose %s failed; inspect protected host logs", operation)
	}
	if out.exceeded {
		return nil, errors.New("Compose output exceeds size limit")
	}
	return out.Bytes(), nil
}

// Resolve includes interpolated secrets. Keep the result private and never log it.
func (e ComposeExecutor) Resolve(ctx context.Context) ([]byte, error) {
	return e.run(ctx, 30*time.Second, "resolve", "--profile", "*", "config", "--format", "json")
}
func (e ComposeExecutor) Validate(ctx context.Context) error {
	_, err := e.run(ctx, 30*time.Second, "validate", "config", "--quiet")
	return err
}

// Apply does not pull or build images, touch database services or remove orphans.
// Recreating web refreshes its backend DNS after server replacement.
func (e ComposeExecutor) Apply(ctx context.Context) error {
	if err := e.Validate(ctx); err != nil {
		return err
	}
	if _, err := e.run(ctx, 3*time.Minute, "apply server", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "server"); err != nil {
		return err
	}
	_, err := e.run(ctx, 3*time.Minute, "refresh web", "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", "120", "web")
	return err
}

// Verify checks Compose's running container state and published port inventory.
// This is not proof of TLS readiness, firewall allowance or public reachability.
func (e ComposeExecutor) Verify(ctx context.Context, r Request) error {
	if r.Protocol == "mp4" {
		return e.verifyMP4(ctx, r.Enabled)
	}
	if r.Protocol == "rdp-drive" {
		return e.verifyDrive(ctx, r.Enabled)
	}
	if r.Protocol == "graphics" {
		if err := Validate(r); err != nil {
			return err
		}
		return e.verifyGraphics(ctx, r.Enabled)
	}
	if err := Validate(r); err != nil {
		return err
	}
	raw, err := e.run(ctx, 30*time.Second, "verify", "ps", "--all", "--format", "json", "server", "web")
	if err != nil {
		return err
	}
	if err = verifyContainers(raw, r); err != nil {
		return err
	}
	if r.Protocol == "management" {
		return e.VerifyManagement(ctx, r.Port)
	}
	return nil
}

type containerStatus struct {
	Service    string
	State      string
	Health     string
	Publishers []struct {
		URL           string
		TargetPort    int
		PublishedPort int
		Protocol      string
	}
}

func verifyContainers(raw []byte, r Request) error {
	var containers []containerStatus
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("no deployed containers found")
	}
	if trimmed[0] == '[' {
		if json.Unmarshal(trimmed, &containers) != nil {
			return errors.New("invalid Compose status")
		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		for {
			var c containerStatus
			err := decoder.Decode(&c)
			if err == io.EOF {
				break
			}
			if err != nil {
				return errors.New("invalid Compose status")
			}
			containers = append(containers, c)
		}
	}
	target, _ := ContainerPort(r.Protocol)
	counts := map[string]int{}
	matches := 0
	for _, c := range containers {
		if c.Service != "server" && c.Service != "web" {
			continue
		}
		counts[c.Service]++
		if c.State != "running" || c.Health != "healthy" {
			return errors.New("server and web must be running and healthy")
		}
		if c.Service != "server" || r.Protocol == "management" {
			continue
		}
		for _, p := range c.Publishers {
			if p.TargetPort != target || p.PublishedPort == 0 {
				continue
			}
			if !r.Enabled {
				return errors.New("disabled protocol is still published")
			}
			if p.Protocol != "tcp" || p.PublishedPort != r.Port || p.URL != r.Bind {
				return errors.New("published protocol mapping differs from requested mapping")
			}
			matches++
		}
	}
	if counts["server"] != 1 || counts["web"] != 1 {
		return errors.New("expected exactly one server and one web container")
	}
	if r.Enabled && r.Protocol != "management" && matches != 1 {
		return errors.New("requested protocol mapping not found")
	}
	return nil
}

func (e ComposeExecutor) CheckApplication(ctx context.Context, r Request) error {
	if specialized(r.Protocol) || r.Protocol == "website" {
		return e.liveApplication(ctx, r, "check")
	}
	return e.applicationProbe(ctx, r, false)
}
func (e ComposeExecutor) VerifyApplication(ctx context.Context, r Request) error {
	if r.Protocol == "website" {
		return e.liveApplication(ctx, r, "verify")
	}
	return e.applicationProbe(ctx, r, true)
}
func (e ComposeExecutor) applicationProbe(ctx context.Context, r Request, verify bool) error {
	if err := Validate(r); err != nil {
		return err
	}
	command := "deployment-check"
	if verify {
		command = "deployment-verify"
	}
	return e.applicationCommand(ctx, r, command)
}
func (e ComposeExecutor) ApplyWebsite(ctx context.Context, r Request) error {
	return e.liveApplication(ctx, r, "site-apply")
}
func (e ComposeExecutor) applicationCommand(ctx context.Context, r Request, command string) error {
	args := []string{"exec", "-T", "server", "/app/nt", command, r.Protocol, strconv.FormatBool(r.Enabled), r.Bind, strconv.Itoa(r.Port)}
	if r.SiteID != "" {
		args = append(args, r.SiteID, strconv.FormatInt(r.SiteVersion, 10), strconv.FormatBool(r.SiteEnabled), r.Actor)
	}
	raw, err := e.run(ctx, 25*time.Second, "application preflight", args...)
	if err != nil {
		return err
	}
	var result struct {
		Passed bool   `json:"passed"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return errors.New("invalid application preflight response")
	}
	if !result.Passed {
		if result.Error == "" {
			return errors.New("application preflight failed")
		}
		return errors.New(result.Error)
	}
	return nil
}

func (e ComposeExecutor) ValidateNginx(ctx context.Context) error {
	_, err := e.run(ctx, 45*time.Second, "Nginx preflight", "run", "--rm", "--no-deps", "--pull", "never", "-T", "--entrypoint", "nginx", "web", "-t")
	return err
}
func (e ComposeExecutor) VerifyManagement(ctx context.Context, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("invalid management port")
	}
	_, err := e.run(ctx, 15*time.Second, "management upstream verification", "exec", "-T", "web", "wget", "-q", "-T", "5", "-O", "/dev/null", fmt.Sprintf("http://server:%d/readyz", port))
	return err
}

func (e ComposeExecutor) liveApplication(ctx context.Context, r Request, operation string) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	out, err := e.run(ctx, 45*time.Second, "live application preflight", "exec", "-T", "server", "/app/nt", "deployment-live-check", string(raw), operation)
	if err != nil {
		return err
	}
	var result struct {
		Passed bool   `json:"passed"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(out, &result) != nil {
		return errors.New("invalid live preflight result")
	}
	if !result.Passed {
		if result.Error == "" {
			return errors.New("live application preflight failed")
		}
		return errors.New(result.Error)
	}
	return nil
}
