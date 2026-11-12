package application

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/migrations"
)

func r11RecoveryEvidence(t *testing.T, app *App, srv *httptest.Server, admin *http.Cookie, user string) {
	t.Helper()
	r09Certificate(t, app, "r11.internal")
	ca := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "R11 recovery CA", "kind": "client_ca", "source": "self_signed", "days": 2}, admin)
	postID(t, srv, "/api/admin/certificates", map[string]any{"name": "R11 old client", "kind": "client", "parentId": ca, "userId": user, "days": 1}, admin)
	input := credInput("r11-version-history")
	id := postID(t, srv, "/api/admin/credentials", input, admin)
	input["version"] = 1
	input["secret"] = map[string]string{"password": "new-test-version"}
	expect(t, call(t, srv.URL, "PUT", "/api/admin/credentials/"+id, input, admin, origin), 200)
}

func TestR11EncryptedDatabaseRoundTrip(t *testing.T) {
	for _, scope := range []string{"control-plane", "full"} {
		t.Run(scope, func(t *testing.T) { r11SnapshotRoundtrip(t, scope) })
	}
}
func r11SnapshotRoundtrip(t *testing.T, scope string) {
	dsn := os.Getenv("NT_BACKUP_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated PG18 tool fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal("fixture PG unavailable")
	}
	defer root.Close(context.Background())
	suffix := identity.NewToken()[:16]
	source := "nt_restore_source_" + suffix
	target := "nt_restore_target_" + suffix
	role := "nt_restore_role_" + suffix
	cleanup := func() {
		for _, db := range []string{source, target} {
			root.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)")
		}
		root.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	}
	defer cleanup()
	if _, e = root.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{source}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	pass := identity.NewToken()
	if _, e = root.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD '"+pass+"'"); e != nil {
		t.Fatal(e)
	}
	if _, e = root.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{target}.Sanitize()+" OWNER "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	config, _ := pgx.ParseConfig(dsn)
	makeDSN := func(db, user, password string) string {
		u := url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Path: "/" + db, RawQuery: "sslmode=disable"}
		return u.String()
	}
	srcDSN := makeDSN(source, config.User, config.Password)
	dstDSN := makeDSN(target, role, pass)
	src, e := pgxpool.New(ctx, srcDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	if e = migrations.Apply(ctx, src); e != nil {
		t.Fatal(e)
	}
	mfa, vault := bytes.Repeat([]byte{42}, 32), bytes.Repeat([]byte{43}, 32)
	cfg := Config{EncryptionKey: mfa, VaultKey: vault, AllowedOrigins: []string{origin}}
	a := New(src, cfg)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	expect(t, call(t, srv.URL, "POST", "/api/setup-user", setupBody(), nil, origin), 204)
	admin := loginAs(t, srv, "accept-admin")
	// Exercise settings added after the original backup schema, not only old
	// credential tables. Restored node heartbeats must not imply a live source.
	_, settingsTag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	settingsResult, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", settingsTag, map[string]any{"set": map[string]any{"watermark-content": "restore-check {user}", "user-client-cert-valid-days": 7}}, admin)
	expect(t, settingsResult, 202)
	_, settingsTag = settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	_, hostPrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	hostDER, e := x509.MarshalPKCS8PrivateKey(hostPrivate)
	if e != nil {
		t.Fatal(e)
	}
	hostPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: hostDER}))
	settingsResult, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", settingsTag, map[string]any{"password": testPassword, "set": map[string]any{"ssh-server-private-key": hostPEM, "mail-enabled": true, "mail-host": "smtp.restore.invalid", "mail-from": "restore@example.invalid", "mail-username": "restore", "mail-password": "r16-restore-secret", "llm-base-url": "https://model.restore.invalid/v1", "llm-model": "restore-model", "llm-api-key": "isolated-model-restore-secret", "llm-profile-enabled": true, "llm-temperature": 0.35}}, admin)
	expect(t, settingsResult, 202)
	if _, e = a.appliedConfiguration(ctx); e != nil {
		t.Fatal(e)
	}
	postID(t, srv, "/api/admin/credentials", credInput("restore-check"), admin)
	if _, e = src.Exec(ctx, `UPDATE instance_state SET mfa_key_fingerprint=$1,vault_key_fingerprint=$2`, identity.Digest(string(mfa)), identity.Digest(string(vault))); e != nil {
		t.Fatal(e)
	}
	if _, e = src.Exec(ctx, `INSERT INTO jobs(kind,state) VALUES('ssh.command','queued')`); e != nil {
		t.Fatal(e)
	}
	var user string
	if e = src.QueryRow(ctx, "SELECT id::text FROM users WHERE username='accept-admin'").Scan(&user); e != nil {
		t.Fatal(e)
	}
	fs := storage.New(a.db)
	r11RecoveryEvidence(t, a, srv, admin, user)
	folder, e := fs.Ensure(ctx, user)
	if e != nil {
		t.Fatal(e)
	}
	actorCtx := storage.WithActor(ctx, user, "web", "")
	node, e := fs.Create(actorCtx, user, folder.ID, "restore-data.txt", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = fs.Write(actorCtx, user, node.ID, 0, []byte("durable-content")); e != nil {
		t.Fatal(e)
	}
	work := t.TempDir()
	bundle := backup.RecoveryBundle{Version: 1, MFAKey: hex.EncodeToString(mfa), VaultKey: hex.EncodeToString(vault)}
	recovery, _ := age.GenerateX25519Identity()
	a.config.BackupDirectory = work
	a.config.DatabaseURL = srcDSN
	bs := backup.New(a.db)
	if e = bs.SaveConfig(ctx, backup.Config{Recipient: recovery.Recipient().String(), RetentionDays: 31, ScheduleTime: "02:00", Timezone: "Asia/Shanghai", Scope: scope, Revision: 1}); e != nil {
		t.Fatal(e)
	}
	requestKey := "11111111-1111-4111-8111-111111111111"
	result := object(t, call(t, srv.URL, "POST", "/api/admin/backup/tasks", map[string]any{"requestKey": requestKey, "drainConfirmed": true}, admin, origin))
	jobID := result["id"].(string)
	repeat := object(t, call(t, srv.URL, "POST", "/api/admin/backup/tasks", map[string]any{"requestKey": requestKey, "drainConfirmed": true}, admin, origin))
	if repeat["id"] != jobID {
		t.Fatal("idempotency violated")
	}
	if e = a.RunBackupOnce(ctx); e != nil {
		t.Fatal(e)
	}
	task, e := bs.Task(ctx, jobID)
	if e != nil || task.State != "succeeded" {
		t.Fatal("backup job failed", task, e)
	}
	archive := filepath.Join(work, jobID+".ntbackup.age")
	downloadPath := "/api/admin/backup/files/" + jobID + "/download"
	expect(t, call(t, srv.URL, "GET", downloadPath, nil, admin, origin), 400)
	proofPath := "/api/admin/backup/files/" + jobID + "/export-ticket"
	expect(t, call(t, srv.URL, "POST", proofPath, map[string]string{"password": "wrong"}, admin, origin), 400)
	ticket := object(t, call(t, srv.URL, "POST", proofPath, map[string]string{"password": testPassword}, admin, origin))["ticket"].(string)
	downloaded := call(t, srv.URL, "GET", downloadPath+"?ticket="+ticket, nil, admin, origin)
	expect(t, downloaded, 200)
	original, e := os.ReadFile(archive)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(downloaded.body, original) {
		t.Fatal("download differs from published archive")
	}
	expect(t, call(t, srv.URL, "GET", downloadPath+"?ticket="+ticket, nil, admin, origin), 400)
	corrupted := bytes.Clone(original)
	corrupted[len(corrupted)-1] ^= 1
	if e = os.WriteFile(archive, corrupted, 0600); e != nil {
		t.Fatal(e)
	}
	ticket = object(t, call(t, srv.URL, "POST", proofPath, map[string]string{"password": testPassword}, admin, origin))["ticket"].(string)
	expect(t, call(t, srv.URL, "GET", downloadPath+"?ticket="+ticket, nil, admin, origin), 409)
	if e = os.WriteFile(archive, original, 0600); e != nil {
		t.Fatal(e)
	}
	file, e := os.Open(archive)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	verified, stage, e := backup.Stage(file, recovery.String(), backup.SupportedSchema, work)
	if e != nil {
		t.Fatal(e)
	}
	log, e := backup.NewRestoreLog(work)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	if e = backup.RestoreStaged(ctx, dstDSN, stage, verified, log); e != nil {
		t.Fatal(e)
	}
	c, e := pgx.Connect(ctx, dstDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	if e = bundle.Verify(ctx, c); e != nil {
		t.Fatal(e)
	}
	if e = backup.VerifyObjects(ctx, c, stage, scope); e != nil {
		t.Fatal(e)
	}
	wrong := bundle
	wrong.VaultKey = hex.EncodeToString(bytes.Repeat([]byte{9}, 32))
	if e = wrong.Verify(ctx, c); e == nil {
		t.Fatal("wrong recovered key accepted")
	}
	dst, e := pgxpool.New(ctx, dstDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer dst.Close()
	restored := New(dst, cfg)
	if e = restored.QuarantineRestoredState(ctx, log.ID, scope); e != nil {
		t.Fatal(e)
	}
	restoredConfiguration, e := restored.system.ConfigurationView(ctx, 0)
	if e != nil || system.ValidateConfiguration(restoredConfiguration.Values) != nil || restoredConfiguration.Values.String("watermark-content") != "restore-check {user}" || restoredConfiguration.Values.Int("user-client-cert-valid-days") != 7 {
		t.Fatal("latest configuration lost during restore", e)
	}
	if restoredConfiguration.Values.Bool("mail-enabled") || restoredConfiguration.Values.String("mail-password") == "" {
		t.Fatal("restored managed mail must preserve sealed credentials and stay disabled")
	}
	if restoredConfiguration.Values.String("ssh-server-private-key") != "" {
		t.Fatal("restore reactivated the source SSH host identity")
	}
	if restoredConfiguration.Values.Bool("llm-profile-enabled") || restoredConfiguration.Values.String("llm-api-key") == "" || restoredConfiguration.Values.Float("llm-temperature") != 0.35 {
		t.Fatal("restored managed model state incorrect")
	}
	var enabledModel bool
	if e = dst.QueryRow(ctx, `SELECT enabled FROM ai_profiles WHERE settings_managed`).Scan(&enabledModel); e != nil || enabledModel {
		t.Fatal("restored model adapter was not quarantined", e)
	}
	var enabledMail bool
	if e = dst.QueryRow(ctx, `SELECT enabled FROM notification_channels WHERE settings_managed AND type='smtp'`).Scan(&enabledMail); e != nil || enabledMail {
		t.Fatal("restored mail adapter was not quarantined", e)
	}
	for _, node := range restoredConfiguration.Nodes {
		if node.Online {
			t.Fatal("source heartbeat survived restore", node.ID)
		}
	}
	var trustedClients int
	if e = dst.QueryRow(ctx, `SELECT count(*) FROM certificates WHERE kind IN ('client','client_ca') AND revoked_at IS NULL`).Scan(&trustedClients); e != nil || trustedClients != 0 {
		t.Fatal("old client certificate trust survived restore", e)
	}
	restoredFS := storage.New(restored.db)
	data, readErr := restoredFS.Read(actorCtx, user, node.ID, 0, 100)
	if scope == "full" {
		if readErr != nil || string(data) != "durable-content" {
			t.Fatal("object payload lost", readErr)
		}
	} else {
		if !errors.Is(readErr, syscall.ENODATA) {
			t.Fatal("missing payload not denied", readErr)
		}
		if e = restoredFS.Write(actorCtx, user, node.ID, 0, []byte("overwrite")); !errors.Is(e, syscall.ENODATA) {
			t.Fatal("missing payload can be overwritten", e)
		}
	}
	restoredHTTP := httptest.NewServer(restored.Handler())
	defer restoredHTTP.Close()
	expect(t, call(t, restoredHTTP.URL, "GET", "/api/admin/credentials", nil, admin, origin), 401)
	fresh := loginAs(t, restoredHTTP, "accept-admin")
	expect(t, call(t, restoredHTTP.URL, "GET", "/api/admin/credentials", nil, fresh, origin), 200)
	var queued int
	if e = dst.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE state IN ('queued','running')").Scan(&queued); e != nil || queued != 0 {
		t.Fatal("restored task replay risk", e)
	}
	if e = log.Append("prepared"); e != nil {
		t.Fatal(e)
	}
	if scope == "full" {
		if fence, e := FenceOffline(ctx, srcDSN); e == nil {
			fence.Close(ctx)
			t.Fatal("online database accepted for offline restore")
		}
		srv.Close()
		src.Close()
		keyFile := filepath.Join(work, "offline.agekey")
		if e = os.WriteFile(keyFile, []byte(recovery.String()), 0600); e != nil {
			t.Fatal(e)
		}
		profilePath, storagePath := filepath.Join(work, "runtime", "active.json"), filepath.Join(work, "storage", "active.json")
		old := runtimeprofile.Profile{DatabaseURL: srcDSN, MFAKey: bundle.MFAKey, VaultKey: bundle.VaultKey, WritesEnabled: true}
		if e = runtimeprofile.Write(profilePath, old); e != nil {
			t.Fatal(e)
		}
		if e = runtimeprofile.AtomicJSON(storagePath, runtimeprofile.Storage{DatabaseURL: srcDSN}); e != nil {
			t.Fatal(e)
		}
		badSource := old
		badSource.VaultKey = hex.EncodeToString(bytes.Repeat([]byte{9}, 32))
		failed, failure := PrepareDeploymentRestore(ctx, archive, keyFile, work, profilePath, storagePath, dsn, badSource)
		unchanged, readError := runtimeprofile.Read(profilePath)
		if failure == nil || !strings.Contains(failure.Error(), "safety backup failed") || failed.Database != "" || readError != nil || unchanged != old {
			t.Fatal("failed safety backup changed the deployment", failure, readError)
		}
		plan, e := PrepareDeploymentRestore(ctx, archive, keyFile, work, profilePath, storagePath, dsn, old)
		if plan.Database != "" {
			defer func() {
				root.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{plan.Database}.Sanitize()+" WITH (FORCE)")
				root.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{plan.Role}.Sanitize())
			}()
		}
		if e != nil {
			t.Fatal("deployment restore", e)
		}
		profile, e := runtimeprofile.Read(profilePath)
		if e != nil || profile.WritesEnabled || profile.RestoreID != plan.ID || !plan.Verified {
			t.Fatal("restore was not frozen and verified", e)
		}
		safety, e := os.Open(plan.SafetyArchive)
		if e != nil {
			t.Fatal(e)
		}
		_, e = backup.InspectArchive(safety, recovery.String(), backup.SupportedSchema)
		safety.Close()
		if e != nil {
			t.Fatal("safety backup invalid", e)
		}
		t.Run("cli_lifecycle", func(t *testing.T) {
			cli := os.Getenv("NT_R11_CLI_TEST_BINARY")
			if cli == "" {
				t.Skip("requires compiled nt CLI fixture")
			}
			for k, v := range map[string]string{"NT_RUNTIME_PROFILE": profilePath, "NT_STORAGE_PROFILE": storagePath, "NT_RESTORE_DIRECTORY": work, "NT_RESTORE_ADMIN_URL": dsn, "NT_DATABASE_URL": srcDSN, "NT_MFA_KEY": bundle.MFAKey, "NT_VAULT_KEY": bundle.VaultKey, "NT_ENV": "development"} {
				t.Setenv(k, v)
			}
			shown, err := exec.CommandContext(ctx, cli, "settings", "show").Output()
			var cliView system.ConfigView
			if err != nil || json.Unmarshal(shown, &cliView) != nil || cliView.Values.String("watermark-content") != "restore-check {user}" {
				t.Fatal("settings CLI cannot read current restored schema", err)
			}
			check := exec.CommandContext(ctx, cli, "settings", "check", "--revision", strconv.FormatInt(cliView.ID, 10), "--operator", "accept-admin", "--reason", "isolated schema compatibility check")
			check.Stdin = strings.NewReader(`{"set":{"watermark-content":"check-only-value"}}`)
			if err = check.Run(); err != nil {
				t.Fatal("settings CLI check failed", err)
			}
			shown, err = exec.CommandContext(ctx, cli, "settings", "show").Output()
			if err != nil || json.Unmarshal(shown, &cliView) != nil || cliView.Values.String("watermark-content") != "restore-check {user}" {
				t.Fatal("CLI check committed its candidate", err)
			}
			run := func(action string, extra ...string) error {
				args := append([]string{"restore", action, "--workspace", work, "--restore-id", plan.ID}, extra...)
				cmd := exec.CommandContext(ctx, cli, args...)
				output, e := cmd.CombinedOutput()
				if e != nil {
					t.Logf("CLI %s refused: %s", action, output)
				}
				return e
			}
			if e = run("rollback"); e != nil {
				t.Fatal(e)
			}
			rolled, e := runtimeprofile.Read(profilePath)
			if e != nil || rolled.DatabaseURL != srcDSN {
				t.Fatal("rollback failed", e)
			}
			dumpPath := filepath.Join(plan.Stage, "database.dump")
			dump, e := os.ReadFile(dumpPath)
			if e != nil {
				t.Fatal(e)
			}
			broken := bytes.Clone(dump)
			broken[0] ^= 1
			os.WriteFile(dumpPath, broken, 0600)
			if e = run("resume"); e == nil {
				t.Fatal("altered staging data accepted")
			}
			os.WriteFile(dumpPath, dump, 0600)
			if e = run("resume"); e != nil {
				t.Fatal("resume", e)
			}
			pool, e := Open(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer pool.Close()
			frozenCfg := cfg
			frozenCfg.DatabaseURL = plan.Next.DatabaseURL
			frozenCfg.RestoreID = plan.ID
			frozenCfg.StartFrozen = true
			frozenCfg.RestoreDirectory = work
			active := New(pool, frozenCfg)
			web := httptest.NewServer(active.Handler())
			defer web.Close()
			liveCtx, stop := context.WithCancel(ctx)
			defer stop()
			active.StartOperationalServices(liveCtx)
			expect(t, call(t, web.URL, "GET", "/api/admin/credentials", nil, admin, origin), 503)
			if e = run("activate", "--health-url", web.URL+"/readyz"); e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(5 * time.Second)
			for active.maintenance.Load() && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if active.maintenance.Load() {
				t.Fatal("activation did not release maintenance")
			}
			expect(t, call(t, web.URL, "GET", "/api/admin/credentials", nil, admin, origin), 401)
			newLogin := loginAs(t, web, "accept-admin")
			response := call(t, web.URL, "GET", "/api/admin/backup/restores", nil, newLogin, origin)
			expect(t, response, 200)
			if bytes.Contains(response.body, []byte(bundle.MFAKey)) || bytes.Contains(response.body, []byte(plan.Next.DatabaseURL)) {
				t.Fatal("restore history leaked private plan")
			}
			var history map[string]any
			if e = json.Unmarshal(response.body, &history); e != nil || history["activeRestoreId"] != plan.ID {
				t.Fatal("independent restore history missing", e)
			}
			if e = run("rollback"); e == nil {
				t.Fatal("post-write rollback accepted")
			}
			if e = run("activate", "--health-url", web.URL+"/readyz"); e != nil {
				t.Fatal("activation was not idempotent", e)
			}
		})
	}
}
