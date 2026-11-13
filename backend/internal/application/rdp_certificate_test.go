package application

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func certificateTarget(t *testing.T) (string, int, string) {
	t.Helper()
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := seed.TLS.Certificates[0]
	seed.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				request := make([]byte, 19)
				if _, e := io.ReadFull(c, request); e != nil {
					return
				}
				if request[11] != 1 || request[15] != 3 {
					return
				}
				c.Write([]byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 2, 0, 8, 0, 2, 0, 0, 0})
				peer := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				_ = peer.Handshake()
			}()
		}
	}()
	return "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, fmt.Sprintf("sha256:%x", sha256.Sum256(certificate.Certificate[0]))
}

func TestRDPCertificateAutomaticSave(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	credential := postID(t, srv, "/api/admin/credentials", credInput("RDP certificate"), admin)
	host, port, fingerprint := certificateTarget(t)
	input := assetInput("Automatic RDP", credential, "")
	input["protocol"] = "rdp"
	input["host"] = host
	input["port"] = port
	probe := call(t, srv.URL, "POST", "/api/admin/rdp/certificate", input, admin, origin)
	expect(t, probe, 200)
	if object(t, probe)["fingerprint"] != fingerprint {
		t.Fatal("incorrect fingerprint")
	}
	id := postID(t, srv, "/api/admin/assets", input, admin)
	saved := call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	expect(t, saved, 200)
	if object(t, saved)["certFingerprint"] != fingerprint {
		t.Fatal("automatic pin not persisted")
	}
	// An unchanged target preserves its pin, rather than trusting a new certificate silently.
	input["version"] = 1
	input["certFingerprint"] = "sha256:existing-pin"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, input, admin, origin), 200)
	_, otherPort, _ := certificateTarget(t)
	input["port"] = otherPort
	input["version"] = 2
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, input, admin, origin), 200)
	saved = call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin)
	if object(t, saved)["certFingerprint"] != fingerprint {
		t.Fatal("changed endpoint retained stale pin")
	}
	input["port"] = 1
	input["certFingerprint"] = ""
	delete(input, "version")
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets", input, admin, origin), 422)
	countAssets(t, srv, admin, "/api/admin/assets/paging", 1)
	postID(t, srv, "/api/admin/users", userInput("cert-denied", nil, nil), admin)
	denied := loginAs(t, srv, "cert-denied")
	expect(t, call(t, srv.URL, "POST", "/api/admin/rdp/certificate", input, denied, origin), 403)
}

func TestRDPLegacyAssetOptIn(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	credential := postID(t, srv, "/api/admin/credentials", credInput("legacy RDP"), admin)
	body := assetInput("legacy RDP", credential, "")
	body["protocol"] = "rdp"
	body["host"] = "127.0.0.1"
	body["port"] = 1
	// No connection can succeed on this target. Default mode must fail closed.
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets", body, admin, origin), 422)
	body["rdpSecurity"] = "rdp"
	body["certFingerprint"] = "sha256:stale"
	id := postID(t, srv, "/api/admin/assets", body, admin)
	saved := object(t, call(t, srv.URL, "GET", "/api/admin/assets/"+id, nil, admin, origin))
	if saved["rdpSecurity"] != "rdp" || saved["certFingerprint"] != "" {
		t.Fatal("legacy policy/pin not persisted correctly")
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/rdp/certificate", body, admin, origin), 422)
	body["version"] = 1
	body["certFingerprint"] = ""
	body["rdpSecurity"] = ""
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, body, admin, origin), 422)
	body["rdpSecurity"] = "auto"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, body, admin, origin), 422)
	body["rdpSecurity"] = "rdp"
	expect(t, call(t, srv.URL, "PUT", "/api/admin/assets/"+id, body, admin, origin), 200)
	postID(t, srv, "/api/admin/users", userInput("legacy-denied", nil, nil), admin)
	denied := loginAs(t, srv, "legacy-denied")
	expect(t, call(t, srv.URL, "POST", "/api/admin/assets", body, denied, origin), 403)
}
