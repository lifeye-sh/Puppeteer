package application

import (
	"bytes"
	"encoding/base64"
	"filippo.io/age"
	"io"
	"testing"
)

func TestR11BackupConfiguration(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	expect(t, call(t, srv.URL, "GET", "/api/admin/backup/config", nil, nil, origin), 401)
	key, _ := age.GenerateX25519Identity()
	initial := object(t, call(t, srv.URL, "GET", "/api/admin/backup/config", nil, admin, origin))
	if initial["retentionDays"].(float64) != 31 {
		t.Fatal("wrong default")
	}
	initial["recipient"] = key.Recipient().String()
	initial["password"] = testPassword
	expect(t, call(t, srv.URL, "PUT", "/api/admin/backup/config", initial, admin, origin), 422)
	challenge := object(t, call(t, srv.URL, "POST", "/api/admin/backup/recipient-challenge", map[string]string{"recipient": key.Recipient().String()}, admin, origin))
	encrypted, e := base64.StdEncoding.DecodeString(challenge["challenge"].(string))
	if e != nil {
		t.Fatal(e)
	}
	plain, e := age.Decrypt(bytes.NewReader(encrypted), key)
	if e != nil {
		t.Fatal(e)
	}
	response, e := io.ReadAll(plain)
	if e != nil {
		t.Fatal(e)
	}
	initial["recipientProofId"] = challenge["id"]
	initial["recipientResponse"] = "wrong"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/backup/config", initial, admin, origin), 422)
	initial["recipientResponse"] = string(response)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/backup/config", initial, admin, origin), 204)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/backup/config", initial, admin, origin), 409)
	initial["revision"] = 2
	initial["recipient"] = "invalid"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/backup/config", initial, admin, origin), 422)
	postID(t, srv, "/api/admin/users", userInput("backup-denied", nil, nil), admin)
	denied := loginAs(t, srv, "backup-denied")
	expect(t, call(t, srv.URL, "GET", "/api/admin/backup/config", nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/backup/files", nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/backup/files", nil, admin, origin), 200)
}
