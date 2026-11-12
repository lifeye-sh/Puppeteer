package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/jobs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real OpenSSH fixture, isolated Docker network, no published port or production
// credentials. Every case provisions its own account and removes it afterwards.
func rotationFixture(t *testing.T) (*ssh.Client, string) {
	t.Helper()
	if os.Getenv("NT_ROTATION_FIXTURE") != "1" {
		t.Skip("requires dedicated rotation-fixture")
	}
	password, e := os.ReadFile("/src/.local/r10-rotation-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	client, e := ssh.Dial("tcp", "rotation-fixture:2222", &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.Password(string(password))}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	clear(password)
	if e != nil {
		t.Fatal(e)
	}
	entropy := make([]byte, 6)
	rand.Read(entropy)
	user := "r10" + hex.EncodeToString(entropy)
	session, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	command := fmt.Sprintf("adduser -D -s /bin/bash %s && mkdir -p /home/%s/.ssh && chmod 700 /home/%s/.ssh && chown %s:%s /home/%s/.ssh && printf '%%s ALL=(root) NOPASSWD: /usr/sbin/chpasswd\\n' %s > /etc/sudoers.d/%s && chmod 440 /etc/sudoers.d/%s", user, user, user, user, user, user, user, user, user)
	if e = session.Run(command); e != nil {
		t.Fatal(e)
	}
	session.Close()
	t.Cleanup(func() {
		s, e := client.NewSession()
		if e == nil {
			_ = s.Run("deluser --remove-home " + user + "; rm -f /etc/sudoers.d/" + user)
			s.Close()
		}
		client.Close()
	})
	return client, user
}
func fixturePassword(t *testing.T, client *ssh.Client, user, password string) {
	t.Helper()
	s, e := client.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Stdin = strings.NewReader(user + ":" + password + "\n")
	if e = s.Run("chpasswd"); e != nil {
		t.Fatal(e)
	}
}
func rotationLiveAsset(t *testing.T, srv *httptest.Server, admin *http.Cookie, kind string) (string, *ssh.Client, string) {
	t.Helper()
	root, user := rotationFixture(t)
	password := "fixture-only-initial-password-rotation"
	fixturePassword(t, root, user, password)
	cred := vault.Write{Credential: vault.Credential{Name: user, Kind: kind, Username: user}, Secret: &vault.Secret{Password: password}}
	if kind == "private-key" {
		cred.Secret = nil
		cred.Generate = true
		if e := vault.Validate(&cred); e != nil {
			t.Fatal(e)
		}
		cred.Generate = false
		fs, e := sftp.NewClient(root)
		if e != nil {
			t.Fatal(e)
		}
		defer fs.Close()
		f, e := fs.Create("/home/" + user + "/.ssh/authorized_keys")
		if e != nil {
			t.Fatal(e)
		}
		// Preserve restrictions through both atomic file replacements.
		if _, e = f.Write([]byte("no-agent-forwarding,no-port-forwarding " + cred.PublicKey + "\n")); e != nil {
			t.Fatal(e)
		}
		f.Close()
		s, _ := root.NewSession()
		e = s.Run("chown " + user + ":" + user + " /home/" + user + "/.ssh/authorized_keys && chmod 600 /home/" + user + "/.ssh/authorized_keys")
		s.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	cid := postID(t, srv, "/api/admin/credentials", cred, admin)
	body := assetInput(user, cid, "")
	body["host"] = "rotation-fixture"
	body["port"] = 2222
	asset := postID(t, srv, "/api/admin/assets", body, admin)
	observation := object(t, call(t, srv.URL, "POST", "/api/admin/assets/"+asset+"/host-key/discover", nil, admin, origin))
	key := observation["observed"].(map[string]any)
	key["assetVersion"] = observation["assetVersion"]
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+asset+"/host-key", key, admin, origin), 204)
	return asset, root, user
}
func TestR10RotationLiveOpenSSH(t *testing.T) {
	if os.Getenv("NT_ROTATION_FIXTURE") != "1" {
		t.Skip("requires dedicated rotation-fixture")
	}
	for _, kind := range []string{"password", "private-key"} {
		t.Run(kind, func(t *testing.T) {
			_, a, srv := r02Server(t, false)
			admin := loginAs(t, srv, "accept-admin")
			asset, _, _ := rotationLiveAsset(t, srv, admin, kind)
			res := call(t, srv.URL, "POST", "/api/rotation-runs", map[string]any{"key": taskKey, "assetIds": []string{asset}}, admin, origin)
			expect(t, res, 201)
			id := object(t, res)["id"].(string)
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- a.RunTaskOnce(context.Background()) }()
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				if e != nil {
					t.Fatal(e)
				}
			}
			run, e := a.vault.RotationRun(context.Background(), id)
			if e != nil || run.State != "succeeded" || run.Targets[0].NewCredentialID == "" {
				t.Fatalf("rotation: %+v error %v", run, e)
			}
			t.Log(kind, "real OpenSSH verified candidate accepted, prior credential rejected, asset rebound")
		})
	}
}
func TestR10RotationLiveLostAcknowledgement(t *testing.T) {
	if os.Getenv("NT_ROTATION_FIXTURE") != "1" {
		t.Skip("requires dedicated rotation-fixture")
	}
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset, root, user := rotationLiveAsset(t, srv, admin, "password")
	res := call(t, srv.URL, "POST", "/api/rotation-runs", map[string]any{"key": taskKey, "assetIds": []string{asset}}, admin, origin)
	expect(t, res, 201)
	id := object(t, res)["id"].(string)
	ctx := context.Background()
	run, e := a.vault.RotationRun(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	target := run.Targets[0]
	job, e := jobs.New(a.db).Claim(ctx, "credential.rotate")
	if e != nil {
		t.Fatal(e)
	}
	var candidate vault.Secret
	e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.vault.MoveRotation(c, job, target.ID, vault.RotationPending, vault.RotationApplying, ""); e != nil {
			return e
		}
		var e error
		candidate, e = a.vault.RotationSecret(c, target.ID, run.ActorID, id)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	// Remote side effect succeeds; simulate process loss before persisting its reply.
	fixturePassword(t, root, user, candidate.Password)
	if _, e = p.Exec(ctx, `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e = a.RecoverTaskJobs(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.RunTaskOnce(ctx); e != nil {
		t.Fatal(e)
	}
	run, e = a.vault.RotationRun(ctx, id)
	if e != nil || run.State != "unknown" {
		t.Fatal(run, e)
	}
	result := call(t, srv.URL, "POST", "/api/rotation-runs/"+id+"/targets/"+target.ID+"/reconcile", nil, admin, origin)
	expect(t, result, 200)
	if object(t, result)["state"] != "succeeded" {
		t.Fatal(string(result.body))
	}
	run, e = a.vault.RotationRun(ctx, id)
	if e != nil || run.State != "succeeded" || run.Targets[0].NewCredentialID == "" {
		t.Fatal(run, e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/rotation-runs/"+id+"/targets/"+target.ID+"/reconcile", nil, admin, origin), 409)
	t.Log("lost reply resolved by authentication probes without another remote mutation")
}

func TestR10RotationLivePartial(t *testing.T) {
	if os.Getenv("NT_ROTATION_FIXTURE") != "1" {
		t.Skip("requires dedicated rotation-fixture")
	}
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	good, _, _ := rotationLiveAsset(t, srv, admin, "password")
	bad, root, user := rotationLiveAsset(t, srv, admin, "password")
	fixturePassword(t, root, user, "changed-outside-platform-password")
	res := call(t, srv.URL, "POST", "/api/rotation-runs", map[string]any{"key": taskKey, "assetIds": []string{good, bad}}, admin, origin)
	expect(t, res, 201)
	id := object(t, res)["id"].(string)
	if e := a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	run, e := a.vault.RotationRun(context.Background(), id)
	if e != nil || run.State != "partial" {
		t.Fatal(run, e)
	}
	for _, target := range run.Targets {
		if target.AssetID == good && target.State != vault.RotationSucceeded {
			t.Fatal(target)
		}
		if target.AssetID == bad && (target.State != vault.RotationFailed || target.NewCredentialID != "") {
			t.Fatal(target)
		}
	}
}

func TestR10RotationLiveScheduledPolicy(t *testing.T) {
	if os.Getenv("NT_ROTATION_FIXTURE") != "1" {
		t.Skip("requires dedicated rotation-fixture")
	}
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset, _, _ := rotationLiveAsset(t, srv, admin, "password")
	input := r10TaskInput(asset, "")
	input["kind"] = "credential.rotate"
	input["cron"] = "* * * * *"
	input["enabled"] = true
	schedule := postID(t, srv, "/api/tasks", input, admin)
	if _, e := p.Exec(context.Background(), `UPDATE schedules SET next_run=now()-interval '2 seconds' WHERE id=$1`, schedule); e != nil {
		t.Fatal(e)
	}
	if e := automation.New(a.db).Tick(context.Background(), jobs.New(a.db)); e != nil {
		t.Fatal(e)
	}
	if e := a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	var runID string
	if e := p.QueryRow(context.Background(), `SELECT id FROM scheduled_runs WHERE schedule_id=$1`, schedule).Scan(&runID); e != nil {
		t.Fatal(e)
	}
	run, e := a.vault.RotationRun(context.Background(), runID)
	if e != nil || run.State != "succeeded" {
		t.Fatal(run, e)
	}
	task, e := automation.New(a.db).Run(context.Background(), runID)
	if e != nil || task.Targets[0].State != "succeeded" {
		t.Fatal(task, e)
	}
	// A second policy execution must snapshot the newly bound credential.
	id := triggerTask(t, srv, admin, schedule, taskKey)
	if e = a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	second, e := a.vault.RotationRun(context.Background(), id)
	if e != nil || second.State != "succeeded" || second.Targets[0].CredentialID != run.Targets[0].NewCredentialID {
		t.Fatal(second, e)
	}
}
