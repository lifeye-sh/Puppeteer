package application

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"next-terminal/backend/internal/modules/identity"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
	"strings"
	"testing"
	"time"
)

func TestR13PersonalCertificates(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	ownerID := postID(t, srv, "/api/admin/users", userInput("cert-owner", nil, nil), admin)
	postID(t, srv, "/api/admin/users", userInput("cert-other", nil, nil), admin)
	owner := loginAs(t, srv, "cert-owner")
	other := loginAs(t, srv, "cert-other")
	ca := postID(t, srv, "/api/admin/certificates", map[string]any{"name": "R13 test CA", "kind": "client_ca", "source": "self_signed", "days": 30}, admin)
	issue := func() string {
		return postID(t, srv, "/api/admin/certificates", map[string]any{"name": "Personal", "kind": "client", "parentId": ca, "userId": ownerID, "days": 7}, admin)
	}
	id := issue()
	path := "/api/account/certificates"
	expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
	listed := object(t, call(t, srv.URL, "GET", path, nil, owner, origin))
	if len(listed["items"].([]any)) != 1 {
		t.Fatal("missing own certificate")
	}
	encoded, _ := json.Marshal(listed)
	if strings.Contains(string(encoded), "PRIVATE KEY") || strings.Contains(string(encoded), ca+`","name"`) {
		t.Fatal("private/CA material leaked")
	}
	if len(object(t, call(t, srv.URL, "GET", path, nil, other, origin))["items"].([]any)) != 0 {
		t.Fatal("foreign certificate listed")
	}
	proof := map[string]any{"password": testPassword, "version": 1, "exportPassword": "test-file-password-13"}
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/p12", proof, other, origin), 404)
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/revoke", proof, other, origin), 404)
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/p12", proof, owner, origin), 403)
	secret, _, owner := enroll(t, srv, owner)
	freshProof := func() map[string]any {
		// Test-only clock reset avoids sleeping between isolated MFA assertions.
		if _, e := pool.Exec(context.Background(), `UPDATE mfa_factors SET last_step=-1 WHERE user_id=$1`, ownerID); e != nil {
			t.Fatal(e)
		}
		return map[string]any{"password": testPassword, "totp": identity.TOTP(secret, time.Now().Unix()/30), "version": 1, "exportPassword": "test-file-password-13"}
	}
	proof = freshProof()
	result := call(t, srv.URL, "POST", path+"/"+id+"/p12", proof, owner, origin)
	expect(t, result, 200)
	key, cert, chain, e := pkcs12.DecodeChain(result.body, "test-file-password-13")
	if e != nil || key == nil || cert == nil || len(chain) != 1 {
		t.Fatal("invalid encrypted P12", e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(chain[0])
	if _, e = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = pkcs12.DecodeChain(result.body, "wrong-password"); e == nil {
		t.Fatal("unencrypted export")
	}
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/p12", proof, owner, origin), 400)
	proof = freshProof()
	proof["version"] = 9
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/revoke", proof, owner, origin), 409)
	proof = freshProof()
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/revoke", proof, owner, origin), 204)
	proof = freshProof()
	proof["version"] = 2
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/p12", proof, owner, origin), 403)
	if _, e = a.certificates().ClientIdentity(context.Background(), ca, []*x509.Certificate{cert}); e == nil {
		t.Fatal("revoked certificate accepted")
	}
	id = issue()
	if _, e = pool.Exec(context.Background(), `UPDATE certificates SET not_after=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "POST", path+"/"+id+"/p12", freshProof(), owner, origin), 403)
}
