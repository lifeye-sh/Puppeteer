//go:build linux

package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"golang.org/x/crypto/md4"
	"io"
	"math/big"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/listener"
	"next-terminal/backend/internal/platform/rdprecording"
	"next-terminal/backend/internal/platform/rdpworker"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

func r14ProbeCertificate(t *testing.T, root, name string, expired bool) (tls.Certificate, string, string, string) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{name}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if expired {
		tpl.NotBefore = time.Now().Add(-2 * time.Hour)
		tpl.NotAfter = time.Now().Add(-time.Hour)
	}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certFile, keyFile := filepath.Join(root, name+".crt"), filepath.Join(root, name+".key")
	if e = os.WriteFile(certFile, certPEM, 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(certFile) })
	if e = os.WriteFile(keyFile, keyPEM, 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(keyFile) })
	cert, e := tls.X509KeyPair(certPEM, keyPEM)
	clear(keyPEM)
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(der)
	return cert, certFile, keyFile, "sha256:" + hex.EncodeToString(digest[:])
}

// Actual FreeRDP client -> C worker -> platform -> UDS bridge -> X.224/TLS
// fixture. The fixture accepts NLA negotiation then intentionally rejects auth.
// Seeing CredSSP bytes proves certificate acceptance, NOT successful NLA login.
func TestR14FreeRDPAuthProbe(t *testing.T) {
	workerBin, clientBin := os.Getenv("NT_RDP_WORKER_BINARY"), os.Getenv("NT_RDP_PROBE_CLIENT")
	if workerBin == "" || clientBin == "" {
		t.Skip("fixed FreeRDP worker image required")
	}
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cred := postID(t, srv, "/api/admin/credentials", credInput("freerdp-probe"), admin)
	root, e := os.MkdirTemp("", "nt-frdp-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	_, frontCert, frontKey, _ := r14ProbeCertificate(t, root, "front", false)
	modes := []string{"valid-pin", "bad-pin", "expired-certificate", "tls-only-target", "bad-ticket", "revoked-ticket", "revoke-during-auth", "control-disconnect"}
	fixture := os.Getenv("NT_RDP_DESKTOP_FIXTURE")
	if fixture != "" {
		modes = append(modes, "record-view", "record-quota", "record-bad-password", "record-revoke", "record-file", "record-managed-file", "record-disabled")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			if mode == "record-disabled" {
				v, e := a.system.Configuration(context.Background(), 0)
				if e != nil {
					t.Fatal(e)
				}
				r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"recording-enabled": false}, "password": testPassword}, admin)
				expect(t, r, 202)
				// This fixture has no configuration worker. ACK the revision just as
				// RunConfiguration does before testing the disabled-recording snapshot.
				if _, e = a.appliedConfiguration(context.Background()); e != nil {
					t.Fatal(e)
				}
				defer func() {
					v, e := a.system.Configuration(context.Background(), 0)
					if e != nil {
						t.Error(e)
						return
					}
					r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"recording-enabled": true}, "password": testPassword}, admin)
					expect(t, r, 202)
				}()
			}

			held := mode == "revoke-during-auth" || mode == "control-disconnect"
			nlaReached := make(chan struct{})
			var targetTimedOut atomic.Bool
			recordView := strings.HasPrefix(mode, "record-")
			targetCert, certFile, keyFile, pin := r14ProbeCertificate(t, root, "target-"+mode, mode == "expired-certificate")
			samFile := filepath.Join(root, "sam-"+mode)
			if recordView {
				// Only the disposable fixture account from credInput is used.
				fixturePassword := "remote-only-secret-R03"
				if mode == "record-bad-password" {
					fixturePassword = "isolated-wrong-password"
				}
				hash := md4.New()
				if err := binary.Write(hash, binary.LittleEndian, utf16.Encode([]rune(fixturePassword))); err != nil {
					t.Fatal(err)
				}
				sam := fmt.Sprintf("tester:::%x:::\n", hash.Sum(nil))
				if err := os.WriteFile(samFile, []byte(sam), 0600); err != nil {
					t.Fatal(err)
				}
			}
			access := nativeFixtureToken(t, a, admin, []string{"assets:read", "sessions:connect"})
			var family, actor string
			if e := pool.QueryRow(context.Background(), `SELECT f.id,f.user_id FROM oauth_token_families f JOIN oauth_tokens t ON t.family_id=f.id WHERE t.token_hash=$1`, identity.Digest(access)).Scan(&family, &actor); e != nil {
				t.Fatal(e)
			}
			target, e := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if e != nil {
				t.Fatal(e)
			}
			defer target.Close()
			var dials, credssp, tlsHandshakes atomic.Int32
			targetDone := make(chan struct{})
			fixtureResult := make(chan string, 1)
			go func() {
				defer close(targetDone)
				conn, e := target.Accept()
				if e != nil {
					if recordView {
						fixtureResult <- "no target connection"
					}
					return
				}
				defer conn.Close()
				dials.Add(1)
				if recordView {
					file, err := conn.(*net.TCPConn).File()
					if err != nil {
						fixtureResult <- err.Error()
						return
					}
					defer file.Close()
					childCtx, stop := context.WithTimeout(context.Background(), 24*time.Second)
					defer stop()
					cmd := exec.CommandContext(childCtx, fixture, certFile, keyFile, samFile)
					cmd.ExtraFiles = []*os.File{file}
					result, err := cmd.CombinedOutput()
					fixtureResult <- fmt.Sprintf("%s status=%v", result, err)
					return
				}
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				h := make([]byte, 4)
				if _, e = io.ReadFull(conn, h); e != nil {
					return
				}
				size := int(binary.BigEndian.Uint16(h[2:]))
				if h[0] != 3 || size < 12 || size > 4096 {
					return
				}
				request := make([]byte, size-4)
				if _, e = io.ReadFull(conn, request); e != nil {
					return
				}
				// PROTOCOL_HYBRID; a TLS-only target must be rejected without fallback.
				response := []byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 2, 0, 8, 0, 2, 0, 0, 0}
				if mode == "tls-only-target" {
					response[15] = 1
				}
				if _, e = conn.Write(response); e != nil {
					return
				}
				secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{targetCert}, MinVersion: tls.VersionTLS12})
				if e = secure.Handshake(); e != nil {
					return
				}
				tlsHandshakes.Add(1)
				defer secure.Close()
				b := make([]byte, 1)
				if _, e = io.ReadFull(secure, b); e == nil && b[0] == 0x30 {
					credssp.Add(1)
					close(nlaReached)
					if held {
						_, readErr := io.Copy(io.Discard, secure)
						var timeout net.Error
						targetTimedOut.Store(errors.As(readErr, &timeout) && timeout.Timeout())
					}
				}
			}()
			input := assetInput("freerdp-"+mode, cred, "")
			input["protocol"] = "rdp"
			input["host"] = "127.0.0.1"
			input["port"] = target.Addr().(*net.TCPAddr).Port
			input["certFingerprint"] = pin
			if mode == "bad-pin" {
				input["certFingerprint"] = "sha256:" + strings.Repeat("ab", 32)
			}
			asset := postID(t, srv, "/api/admin/assets", input, admin)
			if mode == "record-file" || mode == "record-managed-file" {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				request := func(token string, want int) string {
					t.Helper()
					req, err := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/native/v1/rdp-file", strings.NewReader(fmt.Sprintf("{\"assetId\":%q}", asset)))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					if token != "" {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					resp, err := srv.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil || resp.StatusCode != want {
						t.Fatalf("RDP file status %d expected %d", resp.StatusCode, want)
					}
					if want == 200 && (!strings.Contains(resp.Header.Get("Cache-Control"), "no-store") || strings.Contains(string(body), "remote-only-secret")) {
						t.Fatal("unsafe RDP download")
					}
					return string(body)
				}
				request("", 403)
				request(access, 409)
				var addr string
				var err error
				if mode == "record-managed-file" {
					a.ConfigureNativeRDP(NativeRDPOptions{Address: "127.0.0.1:0", Worker: workerBin, Certificate: frontCert, PrivateKey: frontKey, FFmpeg: "/usr/bin/ffmpeg"})
					a.listenerManager = listener.New(ctx, 8)
					defer func() {
						a.listenerManager.Close()
						a.listenerManager.Wait()
						a.listenerManager = nil
						a.managedRDP = nil
						a.nativeRDPAddress.Store(nil)
					}()
					_, err = a.appliedConfiguration(ctx)
					if endpoint := a.nativeRDPAddress.Load(); endpoint != nil {
						addr = *endpoint
					}
				} else {
					addr, err = a.StartNativeRDP(ctx, "127.0.0.1:0", workerBin, frontCert, frontKey, "/usr/bin/ffmpeg")
				}
				if err != nil {
					t.Fatal(err)
				}
				file := request(access, 200)
				var ticket string
				for _, line := range strings.Split(file, "\r\n") {
					if strings.HasPrefix(line, "username:s:") {
						ticket = strings.TrimPrefix(line, "username:s:")
					}
				}
				if !strings.Contains(file, "full address:s:"+addr+"\r\n") || len(ticket) != 109 {
					t.Fatal("invalid connection file")
				}
				h, p, _ := net.SplitHostPort(addr)
				client := exec.CommandContext(ctx, clientBin, h, p, frontCert, "--check-pixels")
				client.Stdin = strings.NewReader(ticket + "\n")
				if err = client.Run(); err != nil {
					t.Fatal("desktop pixel verification", err)
				}
				<-targetDone
				id := ticket[8:44]
				for {
					s, err := a.sessions.Get(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					if s.ClosedAt != nil {
						if !s.Readonly || s.State != "closed" {
							t.Fatalf("unexpected session state %s", s.State)
						}
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(50 * time.Millisecond):
					}
				}
				page, err := a.audit.Recording(ctx, id, 0)
				if err != nil || page.Format != audit.MediaFormat || page.Bytes == 0 {
					t.Fatalf("missing recording: %v", err)
				}
				return
			}
			var id, ticket string
			e = a.db.Within(context.Background(), func(ctx context.Context) error {
				var e error
				id, e = a.sessions.Reserve(ctx, session.Session{ActorID: actor, AssetID: asset, Protocol: "rdp", ClientIP: "127.0.0.1", AssetVersion: 1, CredentialVersion: 1, HostKeyVersion: 1}, "", "auth")
				if e != nil {
					return e
				}
				ticket = "NTICKET:" + id + ":" + identity.NewToken()
				if e = a.sessions.IssueNativeRDPTicket(ctx, id, a.nativeRDPTicketHash(ticket), family); e != nil {
					return e
				}
				return a.audit.OpenAccess(ctx, id, actor, asset, "accept-admin", "freerdp-probe", "fixture", "127.0.0.1")
			})
			if e != nil {
				t.Fatal(e)
			}
			if mode == "revoked-ticket" {
				if _, e = pool.Exec(context.Background(), `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "bad-ticket" {
				ticket = "not-a-platform-ticket"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			path := filepath.Join(root, "control.sock")
			t.Cleanup(func() {
				if t.Failed() {
					trace, _ := os.ReadFile(path + ".trace")
					t.Logf("probe stages: %s", trace)
				}
			})
			control, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if e != nil {
				t.Fatal(e)
			}
			defer control.Close()
			profile := "--auth-probe"
			factory := a.nativeRDPControlFactory(ctx, root)
			if recordView {
				profile = "--record-view"
				quota := int64(8 << 20)
				if mode == "record-quota" {
					quota = 100
				}
				factory = a.nativeRDPRecordingFactory(ctx, root, &rdprecording.Options{Root: root, FFmpeg: "/usr/bin/ffmpeg", MaxBytes: quota, IOTimeout: 5 * time.Second})
			}
			supervisor, e := rdpworker.New(workerBin, []string{profile, path, frontCert, frontKey}, 1, 5*time.Second, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			controlDone := make(chan error, 1)
			controlConn := make(chan *net.UnixConn, 1)
			go func() {
				c, e := control.AcceptUnix()
				if e != nil {
					controlDone <- e
					return
				}
				controlConn <- c
				controlDone <- supervisor.ServeControlFactory(ctx, c, factory)
			}()
			mutationDone := make(chan error, 1)
			if held {
				go func() {
					select {
					case <-nlaReached:
					case <-ctx.Done():
						mutationDone <- ctx.Err()
						return
					}
					var before, after time.Time
					err := pool.QueryRow(ctx, `SELECT lease_expires_at FROM sessions WHERE id=$1`, id).Scan(&before)
					if err != nil {
						mutationDone <- err
						return
					}
					select {
					case <-time.After(3 * time.Second):
					case <-ctx.Done():
						mutationDone <- ctx.Err()
						return
					}
					err = pool.QueryRow(ctx, `SELECT lease_expires_at FROM sessions WHERE id=$1`, id).Scan(&after)
					if err != nil {
						mutationDone <- err
						return
					}
					if !after.After(before) {
						mutationDone <- fmt.Errorf("C worker did not renew lease")
						return
					}
					if mode == "revoke-during-auth" {
						_, err = pool.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family)
					} else {
						(<-controlConn).Close()
					}
					mutationDone <- err
				}()
			}
			entry, e := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if e != nil {
				t.Fatal(e)
			}
			defer entry.Close()
			clientArgs := []string{"127.0.0.1", strconv.Itoa(entry.Addr().(*net.TCPAddr).Port), frontCert}
			if mode == "record-view" || mode == "record-disabled" {
				clientArgs = append(clientArgs, "--check-pixels")
			} else if mode == "record-quota" || mode == "record-revoke" {
				clientArgs = append(clientArgs, "--watch")
			}
			client := exec.CommandContext(ctx, clientBin, clientArgs...)
			client.Stdin = strings.NewReader(ticket + "\n")
			if e = client.Start(); e != nil {
				t.Fatal(e)
			}
			entry.SetDeadline(time.Now().Add(5 * time.Second))
			accepted, e := entry.AcceptTCP()
			if e != nil {
				client.Process.Kill()
				client.Wait()
				t.Fatal(e)
			}
			revoked := make(chan error, 1)
			if mode == "record-revoke" {
				go func() {
					tick := time.NewTicker(50 * time.Millisecond)
					defer tick.Stop()
					for {
						select {
						case <-tick.C:
							s, err := a.sessions.Get(ctx, id)
							if err != nil {
								revoked <- err
								return
							}
							if s.State == "connected" {
								_, err = pool.Exec(ctx, `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family)
								revoked <- err
								return
							}
						case <-ctx.Done():
							revoked <- ctx.Err()
							return
						}
					}
				}()
			}
			started := time.Now()
			runErr := supervisor.Run(ctx, accepted)
			workerRuntime := time.Since(started)
			if runErr != nil {
				cancel()
			}
			clientErr := client.Wait()
			control.Close()
			controlErr := <-controlDone
			target.Close()
			<-targetDone
			if recordView {
				diag := <-fixtureResult
				if mode == "record-bad-password" {
					if runErr == nil || !strings.Contains(diag, "authenticated=0 colors=0") {
						t.Fatalf("invalid password accepted: %s %v", diag, runErr)
					}
					if _, e := os.Stat(filepath.Join(root, id, "manifest.json")); !errors.Is(e, os.ErrNotExist) {
						t.Fatal("unauthorized target opened recorder")
					}
					return
				}
				if !strings.Contains(diag, "authenticated=1") || !strings.Contains(diag, "status=<nil>") {
					t.Fatalf("NLA fixture %s, worker %v, client %v", diag, runErr, clientErr)
				}
				if mode == "record-disabled" {
					if runErr != nil || clientErr != nil {
						t.Fatal("unrecorded desktop failed", runErr, clientErr)
					}
					v, err := a.sessions.Get(context.Background(), id)
					if err != nil || v.ConnectedAt == nil || v.ClosedAt == nil {
						t.Fatal("unrecorded connection lifecycle", err)
					}
					access, err := a.audit.Access(context.Background(), id)
					if err != nil || access.RecordingState != "disabled" || access.RecordingBytes != 0 || access.RecordingEvents != 0 {
						t.Fatal("unrecorded desktop stored video", access, err)
					}
					if _, err = os.Stat(filepath.Join(root, id)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("disabled native recorder wrote files", err)
					}
					expect(t, call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, admin, origin), 409)
					return
				}
				raw, err := os.ReadFile(filepath.Join(root, id, "manifest.json"))
				if err != nil {
					t.Fatalf("manifest: %v; worker %v, client %v, fixture %s", err, runErr, clientErr, diag)
				}
				var m rdprecording.Manifest
				if err = json.Unmarshal(raw, &m); err != nil || m.SessionID != id {
					t.Fatalf("recording session mismatch: %v", err)
				}
				if mode == "record-revoke" {
					if err := <-revoked; err != nil {
						t.Fatal(err)
					}
					if runErr == nil || m.State != "incomplete" || m.Reason != "cancelled" || workerRuntime > 8*time.Second {
						t.Fatalf("revocation state=%s reason=%s runtime=%s worker=%v", m.State, m.Reason, workerRuntime, runErr)
					}
				} else if mode == "record-quota" {
					if runErr == nil || m.State != "incomplete" || m.Reason != "quota_exceeded" || workerRuntime > 8*time.Second {
						t.Fatalf("quota: state=%s reason=%s worker=%v", m.State, m.Reason, runErr)
					}
				} else {
					if runErr != nil || clientErr != nil || m.State != "complete" || len(m.Segments) != 1 || m.Frames < 5 {
						t.Fatalf("desktop: %s, worker=%v client=%v fixture=%s", raw, runErr, clientErr, diag)
					}
					path := "/api/sessions/" + id + "/recording"
					index := call(t, srv.URL, "GET", path, nil, admin, origin)
					expect(t, index, 200)
					var page audit.RecordingPage
					if err := json.Unmarshal(index.body, &page); err != nil || page.Format != audit.MediaFormat || page.State != "complete" || len(page.Segments) != 1 || page.DurationMS != m.DurationMS || page.Bytes != m.Bytes || len(page.Events) != 0 {
						t.Fatalf("bad media index: %s", index.body)
					}
					path += "/segments/1"
					postID(t, srv, "/api/admin/users", userInput("media-denied", nil, nil), admin)
					denied := loginAs(t, srv, "media-denied")
					expect(t, call(t, srv.URL, "GET", path, nil, denied, origin), 403)
					expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
					download := call(t, srv.URL, "GET", path+"?download=1", nil, admin, origin)
					expect(t, download, 200)
					digest := sha256.Sum256(download.body)
					if hex.EncodeToString(digest[:]) != m.Segments[0].SHA256 {
						t.Fatal("download checksum mismatch")
					}
					req, _ := http.NewRequest("GET", srv.URL+path, nil)
					req.AddCookie(admin)
					req.Header.Set("Range", "bytes=4-15")
					res, err := srv.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					part, err := io.ReadAll(res.Body)
					res.Body.Close()
					if err != nil || res.StatusCode != 206 || !bytes.Equal(part, download.body[4:16]) {
						t.Fatal("incorrect MP4 range")
					}
					if _, err = os.Stat(filepath.Join(root, id, m.Segments[0].File)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("published video still in spool")
					}
					if dir := os.Getenv("NT_TEST_NATIVE_MEDIA_OUTPUT"); dir != "" {
						if err = os.WriteFile(filepath.Join(dir, "sample.mp4"), download.body, 0600); err != nil {
							t.Fatal(err)
						}
						if err = os.WriteFile(filepath.Join(dir, "index.json"), index.body, 0600); err != nil {
							t.Fatal(err)
						}
					}
					decode := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-i", "pipe:0", "-vf", "crop=2:2:8:8", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
					decode.Stdin = bytes.NewReader(download.body)
					pixels, err := decode.Output()
					if err != nil {
						t.Fatal(err)
					}
					red, blue := false, false
					for i := 0; i+11 < len(pixels); i += 12 {
						red = red || (pixels[i] > 200 && pixels[i+2] < 40)
						blue = blue || (red && pixels[i+2] > 200 && pixels[i] < 40)
					}
					if !blue {
						t.Fatal("recording did not reproduce the client's red to blue transition")
					}
					if renderer := os.Getenv("NT_MP4_TEST_URL"); renderer != "" {
						a.config.MP4URL = renderer
						a.config.MP4Token = os.Getenv("NT_MP4_TEST_TOKEN")
						a.config.MP4Directory = t.TempDir()
						assertExportAfterDisconnect(t, a, srv, id, admin)
					}
					if _, err := pool.Exec(ctx, `UPDATE recording_chunks SET payload=set_byte(payload,0,255) WHERE session_id=$1 AND sequence=1`, id); err != nil {
						t.Fatal(err)
					}
					expect(t, call(t, srv.URL, "GET", path, nil, admin, origin), 409)
				}
				v, err := a.sessions.Get(context.Background(), id)
				if err != nil || v.ClosedAt == nil {
					t.Fatalf("native session not finalized: %v", err)
				}
				if (mode == "record-view" || mode == "record-revoke") && v.ConnectedAt == nil {
					t.Fatal("session never reached connected")
				}
				return
			}
			if held {
				if e := <-mutationDone; e != nil {
					t.Fatal(e)
				}
				if targetTimedOut.Load() {
					t.Fatal("authorization loss did not close target")
				}
			}
			if runErr != nil || clientErr != nil {
				t.Fatalf("worker=%v client=%v", runErr, clientErr)
			}
			wantDials := int32(0)
			if mode != "bad-ticket" && mode != "revoked-ticket" {
				wantDials = 1
				if controlErr != nil && !held {
					t.Fatalf("control: %v", controlErr)
				}
			}
			if dials.Load() != wantDials {
				t.Fatalf("target dials %d want %d", dials.Load(), wantDials)
			}
			wantNLA := int32(0)
			if mode == "valid-pin" || held {
				wantNLA = 1
			}
			if credssp.Load() != wantNLA {
				t.Fatalf("CredSSP %d want %d", credssp.Load(), wantNLA)
			}
			if mode == "tls-only-target" && tlsHandshakes.Load() != 0 {
				t.Fatal("worker downgraded from NLA to TLS")
			}
			var consumed bool
			if e = pool.QueryRow(context.Background(), `SELECT consumed_at IS NOT NULL FROM native_rdp_tickets WHERE session_id=$1`, id).Scan(&consumed); e != nil {
				t.Fatal(e)
			}
			if consumed != (wantDials == 1) {
				t.Fatalf("ticket consumed=%v", consumed)
			}
			if consumed {
				v, err := a.sessions.Get(context.Background(), id)
				if err != nil || v.ClosedAt == nil || v.State != "interrupted" {
					t.Fatalf("worker did not finalize interrupted session: %s %v", v.State, err)
				}
				var workerID string
				if e = pool.QueryRow(context.Background(), `SELECT worker_id FROM native_rdp_tickets WHERE session_id=$1`, id).Scan(&workerID); e != nil {
					t.Fatal(e)
				}
				if _, _, e = a.consumeNativeRDPTicket(context.Background(), ticket, workerID, "127.0.0.1"); e == nil {
					t.Fatal("target auth failure restored ticket")
				}
			}
			var auditCount int
			wantAudit := 0
			if consumed {
				wantAudit = 3
			}
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE details->>'sessionId'=$1 AND action IN ('native.rdp_ticket_consumed','native.rdp_target_prepared','native.rdp_worker_finalized')`, id).Scan(&auditCount); e != nil || auditCount != wantAudit {
				t.Fatalf("audit count=%d want=%d: %v", auditCount, wantAudit, e)
			}
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE details::text LIKE '%remote-only-secret-R03%'`).Scan(&auditCount); e != nil || auditCount != 0 {
				t.Fatal("secret in operation audit")
			}
			// Reserved invalid tickets are closed so the next fixture does not hit limits.
			a.finishSession(id, "interrupted", "test_complete")
		})
	}
}
