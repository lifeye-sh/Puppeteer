package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/webdav"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in temporary intranet fixture for exercising the actual browser forms.
func TestR11WebDAVBrowserFixture(t *testing.T) {
	if os.Getenv("NT_R11_WEBDAV_FIXTURE") != "1" {
		t.Skip("requires explicit browser fixture")
	}
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, "next-terminal"), 0700); e != nil {
		t.Fatal(e)
	}
	handler := &webdav.Handler{FileSystem: webdav.Dir(dir), LockSystem: webdav.NewMemLS()}
	listener, e := net.Listen("tcp", ":8080")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "backup" || password != "test-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})}
	defer server.Close()
	go server.Serve(listener)
	t.Log("temporary authenticated WebDAV fixture ready")
	<-time.After(40 * time.Minute)
}

func TestRemoteArchiveCopies(t *testing.T) {
	for _, kind := range []string{"webdav", "sftp", "s3", "s3-multipart"} {
		t.Run(kind, func(t *testing.T) {
			var d Destination
			switch kind {
			case "webdav":
				h := &webdav.Handler{FileSystem: webdav.Dir(t.TempDir()), LockSystem: webdav.NewMemLS()}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					u, p, ok := r.BasicAuth()
					if !ok || u != "backup" || p != "test-password" {
						w.WriteHeader(401)
						return
					}
					h.ServeHTTP(w, r)
				}))
				t.Cleanup(srv.Close)
				d = Destination{Name: "fixture", Kind: kind, Endpoint: srv.URL, Username: "backup", Secret: "test-password", AllowHTTP: true}
			case "sftp":
				d = sftpFixture(t)
			case "s3", "s3-multipart":
				d = s3Fixture(t)
			}
			payload := bytes.Repeat([]byte("encrypted archive fixture\n"), 10000)
			if kind == "s3-multipart" {
				payload = bytes.Repeat([]byte("encrypted archive fixture\n"), 900000)
			}
			sum := sha256.Sum256(payload)
			digest := hex.EncodeToString(sum[:])
			local := filepath.Join(t.TempDir(), "archive.age")
			if e := os.WriteFile(local, payload, 0600); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			n, e := CopyRemote(ctx, d, local, "one.ntbackup.age", digest)
			if e != nil || n != int64(len(payload)) {
				t.Fatal("copy", n, e)
			}
			if _, e = CopyRemote(ctx, d, local, "one.ntbackup.age", digest); e == nil {
				t.Fatal("existing archive overwritten")
			}
			if _, e = CopyRemote(ctx, d, local, "corrupt.ntbackup.age", strings.Repeat("0", 64)); e == nil {
				t.Fatal("wrong digest accepted")
			}
			r, e := openRemote(ctx, d)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			f, e := r.Open(ctx, "one.ntbackup.age")
			if e != nil {
				t.Fatal(e)
			}
			got, e := io.ReadAll(f)
			f.Close()
			if e != nil || !bytes.Equal(got, payload) {
				t.Fatal("existing archive changed", e)
			}
			if e = RemoveRemote(ctx, d, "one.ntbackup.age"); e != nil {
				t.Fatal(e)
			}
			if kind == "sftp" {
				d.HostKey = "SHA256:wrong"
				if _, e = openRemote(ctx, d); e == nil {
					t.Fatal("wrong SSH host key accepted")
				}
			}
		})
	}
}
func sftpFixture(t *testing.T) Destination {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(key)
	if e != nil {
		t.Fatal(e)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() != "backup" || string(p) != "test-password" {
			return nil, fmt.Errorf("denied")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				c, ch, req, e := ssh.NewServerConn(conn, cfg)
				if e != nil {
					return
				}
				defer c.Close()
				go ssh.DiscardRequests(req)
				for incoming := range ch {
					if incoming.ChannelType() != "session" {
						incoming.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, requests, e := incoming.Accept()
					if e != nil {
						return
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							var sub struct{ Name string }
							ok := request.Type == "subsystem" && ssh.Unmarshal(request.Payload, &sub) == nil && sub.Name == "sftp"
							request.Reply(ok, nil)
							if ok {
								server, e := sftp.NewServer(channel)
								if e == nil {
									server.Serve()
									server.Close()
								}
								return
							}
						}
					}()
				}
			}()
		}
	}()
	return Destination{Name: "fixture", Kind: "sftp", Endpoint: listener.Addr().String(), Username: "backup", Secret: "test-password", HostKey: ssh.FingerprintSHA256(signer.PublicKey()), Prefix: filepath.Join(t.TempDir(), "copies")}
}

// A local S3 protocol fixture exercises the SDK's signed PUT/GET/HEAD/COPY/DELETE
// path. It is not a substitute for provider-specific multipart integration tests.
func s3Fixture(t *testing.T) Destination {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	parts := map[string]map[int][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=fixture-key/") {
			w.WriteHeader(403)
			return
		}
		key := r.URL.Path
		fail := func(code int, name string) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(code)
			fmt.Fprintf(w, "<Error><Code>%s</Code><Message>fixture</Message><Key>%s</Key></Error>", name, key)
		}
		switch r.Method {
		case "POST":
			w.Header().Set("Content-Type", "application/xml")
			if r.URL.Query().Has("uploads") {
				parts[key] = map[int][]byte{}
				fmt.Fprintf(w, `<InitiateMultipartUploadResult><Bucket>backups</Bucket><Key>%s</Key><UploadId>fixture-upload</UploadId></InitiateMultipartUploadResult>`, key)
				return
			}
			if r.URL.Query().Get("uploadId") == "fixture-upload" {
				var data []byte
				for i := 1; i <= len(parts[key]); i++ {
					data = append(data, parts[key][i]...)
				}
				objects[key] = data
				delete(parts, key)
				io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>backups</Bucket><ETag>"fixture-etag"</ETag></CompleteMultipartUploadResult>`)
				return
			}
			fail(400, "InvalidRequest")
		case "PUT":
			if source := r.Header.Get("X-Amz-Copy-Source"); source != "" {
				source, _ = url.PathUnescape(source)
				if !strings.HasPrefix(source, "/") {
					source = "/" + source
				}
				data, ok := objects[source]
				if !ok {
					fail(404, "NoSuchKey")
					return
				}
				objects[key] = bytes.Clone(data)
				w.Header().Set("Content-Type", "application/xml")
				io.WriteString(w, `<CopyObjectResult><LastModified>2026-10-30T00:00:00Z</LastModified><ETag>"fixture-etag"</ETag></CopyObjectResult>`)
				return
			}
			if _, ok := objects[key]; ok && r.Header.Get("If-None-Match") == "*" {
				fail(412, "PreconditionFailed")
				return
			}
			data, e := io.ReadAll(r.Body)
			if e != nil {
				fail(400, "InvalidRequest")
				return
			}
			if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
				reader := bufio.NewReader(bytes.NewReader(data))
				var decoded bytes.Buffer
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						fail(400, "InvalidRequest")
						return
					}
					length, err := strconv.ParseInt(strings.Split(strings.TrimSpace(line), ";")[0], 16, 64)
					if err != nil || length < 0 || length > 1<<20 {
						fail(400, "InvalidRequest")
						return
					}
					if length == 0 {
						break
					}
					if _, err = io.CopyN(&decoded, reader, length); err != nil {
						fail(400, "InvalidRequest")
						return
					}
					if _, err = reader.ReadString('\n'); err != nil {
						fail(400, "InvalidRequest")
						return
					}
				}
				data = decoded.Bytes()
			}
			if r.URL.Query().Get("uploadId") == "fixture-upload" {
				number, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
				parts[key][number] = data
			} else {
				objects[key] = data
			}
			w.Header().Set("ETag", `"fixture-etag"`)
			w.WriteHeader(200)
		case "HEAD", "GET":
			data, ok := objects[key]
			if !ok {
				fail(404, "NoSuchKey")
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Header().Set("Last-Modified", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
			w.Header().Set("ETag", `"fixture-etag"`)
			if r.Method == "GET" {
				w.Write(data)
			}
		case "DELETE":
			if r.URL.Query().Get("uploadId") != "" {
				delete(parts, key)
				w.WriteHeader(204)
				return
			}
			delete(objects, key)
			w.WriteHeader(204)
		default:
			fail(405, "MethodNotAllowed")
		}
	}))
	t.Cleanup(srv.Close)
	return Destination{Name: "fixture", Kind: "s3", Endpoint: srv.URL, Bucket: "backups", Username: "fixture-key", Secret: "fixture-secret", AllowHTTP: true}
}
