package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"puppeteer/backend/internal/modules/backup"
	"puppeteer/backend/internal/modules/identity"
	"puppeteer/backend/internal/platform/postgres"
)

func TestManagedRecoveryBootstrap(t *testing.T) {
	p := testPool(t)
	s := backup.New(postgres.New(p))
	ctx := context.Background()
	key := bytes.Repeat([]byte{43}, 32)
	if err := s.EnsureRecoveryKey(ctx, []byte("invalid")); err == nil {
		t.Fatal("invalid vault key accepted")
	}
	cfg, err := s.Config(ctx)
	if err != nil || cfg.Recipient != "" {
		t.Fatal("failed initialization changed config", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.EnsureRecoveryKey(ctx, key); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cfg, err = s.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := s.RecoveryKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Recipient != cfg.Recipient || !keys[0].Active {
		t.Fatal("concurrent bootstrap must create one active key", err)
	}
	plain, err := s.RecoveryIdentity(ctx, key, keys[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err = p.QueryRow(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1`, keys[0].ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) || bytes.Contains(sealed, []byte("AGE-SECRET-KEY")) {
		t.Fatal("private key stored unencrypted")
	}
	// The existing restore verifier must accept the managed key's ciphertext/AAD.
	mfa := bytes.Repeat([]byte{42}, 32)
	if _, err = p.Exec(ctx, `UPDATE instance_state SET mfa_key_fingerprint=$1,vault_key_fingerprint=$2`, identity.Digest(string(mfa)), identity.Digest(string(key))); err != nil {
		t.Fatal(err)
	}
	conn, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = (backup.RecoveryBundle{Version: 1, MFAKey: hex.EncodeToString(mfa), VaultKey: hex.EncodeToString(key)}).Verify(ctx, conn.Conn())
	conn.Release()
	if err != nil {
		t.Fatal("managed recovery key broke restore verification", err)
	}
	if _, err = s.RecoveryIdentity(ctx, bytes.Repeat([]byte{99}, 32), keys[0].ID); err == nil {
		t.Fatal("wrong vault key accepted")
	}
	if err = s.EnsureRecoveryKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Config(ctx)
	if after != cfg {
		t.Fatal("restart changed recovery config")
	}
	external, _ := age.GenerateX25519Identity()
	cfg.Recipient = external.Recipient().String()
	if err = s.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureRecoveryKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	after, _ = s.Config(ctx)
	if after.Recipient != cfg.Recipient {
		t.Fatal("existing external recipient replaced")
	}
	keys, _ = s.RecoveryKeys(ctx)
	if len(keys) != 1 || keys[0].Active {
		t.Fatal("historical key must remain available")
	}
}

func TestManagedRecoveryAPI(t *testing.T) {
	p, a, srv := r02Server(t, false)
	ctx := context.Background()
	if err := a.InitializeBackupRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, srv, "accept-admin")
	endpoint := "/api/admin/backup/recovery-keys"
	expect(t, call(t, srv.URL, "GET", endpoint, nil, nil, origin), 401)
	listed := call(t, srv.URL, "GET", endpoint, nil, admin, origin)
	expect(t, listed, 200)
	var keys []backup.RecoveryKey
	if err := json.Unmarshal(listed.body, &keys); err != nil || len(keys) != 1 {
		t.Fatal("key list", err)
	}
	if bytes.Contains(listed.body, []byte("AGE-SECRET-KEY")) {
		t.Fatal("private material in list")
	}
	first := keys[0]
	download := endpoint + "/" + first.ID + "/download"
	bad := call(t, srv.URL, "POST", download, map[string]string{"password": "wrong"}, admin, origin)
	if bad.status < 400 {
		t.Fatal("download allowed without proof")
	}
	good := call(t, srv.URL, "POST", download, map[string]string{"password": testPassword}, admin, origin)
	expect(t, good, 200)
	body := object(t, good)
	private := body["privateKey"].(string)
	recovery, err := age.ParseX25519Identity(strings.TrimSpace(private))
	if err != nil || recovery.Recipient().String() != first.Recipient {
		t.Fatal("download does not match recipient", err)
	}
	cfg := object(t, call(t, srv.URL, "GET", "/api/admin/backup/config", nil, admin, origin))
	revision := cfg["revision"]
	// Automatically generated configuration saves without an offline challenge.
	cfg["password"] = testPassword
	expect(t, call(t, srv.URL, "PUT", "/api/admin/backup/config", cfg, admin, origin), 204)
	rotate := map[string]any{"revision": revision, "password": testPassword}
	expect(t, call(t, srv.URL, "POST", endpoint, rotate, admin, origin), 409)
	cfg = object(t, call(t, srv.URL, "GET", "/api/admin/backup/config", nil, admin, origin))
	rotate["revision"] = cfg["revision"]
	fresh := call(t, srv.URL, "POST", endpoint, rotate, admin, origin)
	expect(t, fresh, 201)
	if bytes.Contains(fresh.body, []byte("AGE-SECRET-KEY")) {
		t.Fatal("rotation exposed private material")
	}
	expect(t, call(t, srv.URL, "POST", endpoint, rotate, admin, origin), 409)
	old := object(t, call(t, srv.URL, "POST", download, map[string]string{"password": testPassword}, admin, origin))
	if old["privateKey"] != private {
		t.Fatal("old private key lost after rotation")
	}
	// Old encrypted content still decrypts after rotation.
	var archive bytes.Buffer
	recipient, _ := age.ParseX25519Recipient(first.Recipient)
	writer, _ := age.Encrypt(&archive, recipient)
	writer.Write([]byte("old backup"))
	writer.Close()
	reader, err := age.Decrypt(&archive, recovery)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := io.ReadAll(reader)
	if string(restored) != "old backup" {
		t.Fatal("old backup cannot recover")
	}
	// A key download cannot be repurposed to read other configuration secrets.
	other := uuid.NewString()
	if _, err = p.Exec(ctx, `INSERT INTO configuration_secrets(id,key,sealed) VALUES($1,'mail-password','private-mail-value')`, other); err != nil {
		t.Fatal(err)
	}
	deniedOther := call(t, srv.URL, "POST", endpoint+"/"+other+"/download", map[string]string{"password": testPassword}, admin, origin)
	if deniedOther.status < 400 || bytes.Contains(deniedOther.body, []byte("private-mail-value")) {
		t.Fatal("unrelated secret exposed")
	}
	postID(t, srv, "/api/admin/users", userInput("recovery-denied", nil, nil), admin)
	denied := loginAs(t, srv, "recovery-denied")
	expect(t, call(t, srv.URL, "GET", endpoint, nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "POST", endpoint, rotate, denied, origin), 403)
	expect(t, call(t, srv.URL, "POST", download, map[string]string{"password": testPassword}, denied, origin), 403)
	var logs string
	if err = p.QueryRow(ctx, `SELECT coalesce(string_agg(details::text,''),'') FROM operation_logs WHERE action LIKE 'backup.recovery_key.%'`).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs, "AGE-SECRET-KEY") || strings.Contains(logs, strings.TrimSpace(private)) {
		t.Fatal("private material in audit")
	}
	// Re-check MFA for each private-key export, including replay prevention.
	secret, _, mfaAdmin := enroll(t, srv, admin)
	if call(t, srv.URL, "POST", download, map[string]string{"password": testPassword}, mfaAdmin, origin).status < 400 {
		t.Fatal("MFA bypass")
	}
	code := freshAdminTOTP(t, a, secret)
	proof := map[string]string{"password": testPassword, "totp": code}
	raw, _ := json.Marshal(proof)
	req, _ := http.NewRequest("POST", srv.URL+download, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.AddCookie(mfaAdmin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private download must require MFA and prohibit caching", resp.StatusCode)
	}
	if call(t, srv.URL, "POST", download, proof, mfaAdmin, origin).status < 400 {
		t.Fatal("MFA replay allowed")
	}
}
