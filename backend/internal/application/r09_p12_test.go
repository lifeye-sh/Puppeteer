package application

import (
	"context"
	"next-terminal/backend/internal/modules/identity"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
	"testing"
	"time"
)

func TestR09P12MFAExportAndReissue(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("p12-user", nil, nil), admin)
	ca := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "p12 CA", "kind": "client_ca", "source": "self_signed", "days": 3}, admin)
	id := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "client", "kind": "client", "parentId": ca, "userId": uid, "days": 1}, admin)
	path := "/api/admin/certificates/" + id + "/p12"
	password := "local-export-password-123"
	expect(t, call(t, srv.URL, "POST", path, map[string]string{"password": testPassword, "exportPassword": password}, admin, origin), 403)
	secret, _, session := enroll(t, srv, admin)
	proof := identity.TOTP(secret, time.Now().Unix()/30+1)
	exported := call(t, srv.URL, "POST", path, map[string]string{"password": testPassword, "totp": proof, "exportPassword": password}, session, origin)
	expect(t, exported, 200)
	key, cert, chain, e := pkcs12.DecodeChain(exported.body, password)
	if e != nil || key == nil || cert == nil || len(chain) != 1 {
		t.Fatal("P12 decode", e)
	}
	if _, _, _, e = pkcs12.DecodeChain(exported.body, "wrong"); e == nil {
		t.Fatal("P12 password ignored")
	}
	expect(t, call(t, srv.URL, "POST", path, map[string]string{"password": testPassword, "totp": proof, "exportPassword": password}, session, origin), 400)
	replacement := postID(t, srv, "/api/admin/certificates/"+id+"/reissue", map[string]int{"version": 1, "days": 1}, session)
	old, e := a.certificates().Get(context.Background(), id)
	if e != nil || old.RevokedAt == nil || replacement == id {
		t.Fatal("reissue did not revoke old", e)
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/certificates/"+id+"/reissue", map[string]int{"version": 1, "days": 1}, session, origin), 409)
}
