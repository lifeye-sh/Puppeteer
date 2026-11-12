package application

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/secretbox"
)

func TestR16ManagedMailSecretLifecycleAndDelivery(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	certificate, ca := ldapLifecycleCertificate(t)
	messages := make(chan string, 8)
	host, port := contactSMTPFixture(t, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, messages)
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	initial := head()
	const secret = "r16-isolated-smtp-secret-never-return"
	values := map[string]any{"mail-enabled": true, "mail-use-ssl": false, "mail-host": host, "mail-port": port, "mail-from": "fixture@example.test", "mail-ca-pem": ca, "mail-password": secret}
	request := func(body map[string]any, want int) result {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), body, admin)
		expect(t, r, want)
		return r
	}
	request(map[string]any{"set": values}, 422)
	request(map[string]any{"set": values, "password": "wrong-proof"}, 400)
	if head().ID != initial.ID {
		t.Fatal("failed proof changed configuration")
	}
	r := request(map[string]any{"set": values, "password": testPassword}, 202)
	current := head()
	ref := current.Values.String("mail-password")
	if ref == "" || ref == secret {
		t.Fatal("plaintext or missing reference")
	}
	if strings.Contains(string(r.body), secret) || strings.Contains(string(r.body), ref) {
		t.Fatal("secret material returned in save response")
	}
	public := object(t, r)
	if public["values"].(map[string]any)["mail-password"] != system.SecretMask || public["secrets"].(map[string]any)["mail-password"].(map[string]any)["hasValue"] != true {
		t.Fatal("missing secret projection")
	}
	var sealed []byte
	if e := pool.QueryRow(ctx, `SELECT sealed FROM configuration_secrets WHERE id=$1`, ref).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	plain, e := secretbox.Open(a.config.VaultKey, sealed, "settings:mail-password:"+ref)
	if e != nil || string(plain) != secret {
		t.Fatal("sealed setting not decryptable", e)
	}
	clear(plain)
	var leaked bool
	if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM configuration_revisions WHERE position($1 in snapshot::text)>0) OR EXISTS(SELECT 1 FROM settings WHERE position($1 in typed_value::text)>0)`, secret).Scan(&leaked); e != nil || leaked {
		t.Fatal("plaintext in configuration", e)
	}
	for _, path := range []string{"/api/settings", "/api/settings/revisions/" + jsonNumber(current.ID), "/api/settings/revisions/" + jsonNumber(current.ID) + "/diff", "/api/admin/properties", "/api/admin/notifications"} {
		r := call(t, srv.URL, "GET", path, nil, admin, origin)
		expect(t, r, 200)
		if strings.Contains(string(r.body), secret) || strings.Contains(string(r.body), ref) {
			t.Fatal("secret/reference exposure", path)
		}
	}
	for _, keep := range []string{"", system.SecretMask} {
		request(map[string]any{"set": map[string]any{"mail-password": keep}}, 202)
		if head().ID != current.ID {
			t.Fatal("keep rotated secret")
		}
	}
	channels, e := a.notifications().Channels(ctx)
	if e != nil || len(channels) != 1 || !channels[0].SettingsManaged || !channels[0].HasSecret || !channels[0].Enabled {
		t.Fatal("managed channel not materialized", e)
	}
	channel := channels[0]
	expect(t, call(t, srv.URL, "PUT", "/api/admin/notifications/channels/"+channel.ID, channel, admin, origin), 409)
	if e = a.notifications().SendVerification(ctx, channel.ID, channel.Version, "recipient@example.test", "r16-verify", "R16 verification"); e != nil {
		t.Fatal(e)
	}
	select {
	case body := <-messages:
		if !strings.Contains(body, "R16 verification") {
			t.Fatal("verification body")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("verification not delivered")
	}
	u, e := a.identity.Current(ctx, admin.Value)
	if e != nil {
		t.Fatal(e)
	}
	postID(t, srv, "/api/admin/notifications/bindings", notification.Binding{UserID: u.ID, ChannelID: channel.ID, Address: "recipient@example.test", Enabled: true}, admin)
	expect(t, call(t, srv.URL, "POST", "/api/admin/notifications/channels/"+channel.ID+"/test", nil, admin, origin), 202)
	if e = a.notifications().DeliverOne(ctx, func(context.Context, string, string) (bool, error) { return true, nil }); e != nil {
		t.Fatal(e)
	}
	select {
	case <-messages:
	case <-time.After(3 * time.Second):
		t.Fatal("managed notification worker did not deliver")
	}
	// A stale revision must roll back even the newly sealed secret row.
	var before int
	pool.QueryRow(ctx, `SELECT count(*) FROM configuration_secrets`).Scan(&before)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(initial.ID), map[string]any{"set": map[string]any{"mail-password": "stale-secret"}, "password": testPassword}, admin)
	expect(t, r, 409)
	var after int
	pool.QueryRow(ctx, `SELECT count(*) FROM configuration_secrets`).Scan(&after)
	if after != before {
		t.Fatal("failed batch leaked secret version")
	}
	request(map[string]any{"set": map[string]any{"mail-insecure-skip-verify": true}}, 422)
	request(map[string]any{"set": map[string]any{"mail-ca-pem": "unexpected text\n" + ca}}, 422)
	request(map[string]any{"set": map[string]any{"mail-enabled": false}, "clearSecrets": []string{"mail-password"}}, 422)
	request(map[string]any{"set": map[string]any{"mail-enabled": false}, "clearSecrets": []string{"mail-password"}, "password": testPassword}, 202)
	if head().Values.String("mail-password") != "" {
		t.Fatal("secret not cleared")
	}
	request(map[string]any{"set": map[string]any{"mail-enabled": true, "mail-username": "mailer"}}, 422)
	channels, e = a.notifications().Channels(ctx)
	if e != nil || channels[0].Enabled || channels[0].HasSecret {
		t.Fatal("clear did not reach channel", e)
	}
	request(map[string]any{"set": values, "password": testPassword}, 202)
	preRestore := head()
	if e = a.db.Within(ctx, func(c context.Context) error {
		if e := a.system.QuarantineRestore(c); e != nil {
			return e
		}
		return a.notifications().QuarantineRestore(c)
	}); e != nil {
		t.Fatal(e)
	}
	if head().Values.Bool("mail-enabled") || head().ID <= preRestore.ID {
		t.Fatal("restore left mail enabled or rewrote history")
	}
	old, e := a.system.Configuration(ctx, preRestore.ID)
	if e != nil || !old.Values.Bool("mail-enabled") {
		t.Fatal("restore mutated source revision", e)
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestR16ManagedMailSecretsRequirePermissionAndFreshProof(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("mail-member", nil, nil), admin)
	member := loginAs(t, srv, "mail-member")
	for _, path := range []string{"/api/settings", "/api/admin/properties"} {
		expect(t, call(t, srv.URL, "GET", path, nil, member, origin), 403)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	patch := map[string]any{"mail-host": "smtp.example.invalid", "mail-from": "fixture@example.invalid", "mail-password": "isolated-proof-secret"}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"password": testPassword, "set": patch}, member)
	expect(t, r, 403)
	secret, _, admin := enroll(t, srv, admin)
	body := map[string]any{"password": testPassword, "set": patch}
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, body, admin)
	expect(t, r, 400)
	body["totp"] = identity.TOTP(secret, time.Now().Unix()/30)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, body, admin)
	expect(t, r, 400)
	body["totp"] = identity.TOTP(secret, time.Now().Unix()/30+1)
	r, tag = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, body, admin)
	expect(t, r, 202)
	current, e := a.system.Configuration(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	// A consumed privileged proof cannot rotate the secret again.
	patch["mail-password"] = "must-not-rotate-with-replayed-proof"
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, body, admin)
	expect(t, r, 400)
	latest, e := a.system.Configuration(context.Background(), 0)
	if e != nil || latest.ID != current.ID {
		t.Fatal("replayed proof published configuration", e)
	}
}
