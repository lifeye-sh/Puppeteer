package backup

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type Destination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	Revision  int64  `json:"revision"`
	Endpoint  string `json:"endpoint"`
	Prefix    string `json:"prefix"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	Username  string `json:"username"`
	HostKey   string `json:"hostKey"`
	CA        string `json:"ca"`
	AllowHTTP bool   `json:"allowHttp"`
	HasSecret bool   `json:"hasSecret"`
	Secret    string `json:"secret,omitempty"`
}

func ValidateDestination(d Destination) error {
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 120 || len(d.Endpoint) > 2048 || len(d.CA) > 65536 || len(d.Secret) > 65536 || len(d.Prefix) > 1024 || strings.ContainsAny(d.Prefix, "\\\x00\r\n") {
		return errors.New("invalid destination")
	}
	for _, part := range strings.Split(d.Prefix, "/") {
		if part == ".." || part == "." {
			return errors.New("invalid destination prefix")
		}
	}
	if d.Kind == "sftp" {
		if _, _, e := net.SplitHostPort(d.Endpoint); e != nil {
			return errors.New("SFTP host:port required")
		}
		if !strings.HasPrefix(d.HostKey, "SHA256:") || d.Username == "" {
			return errors.New("SFTP username and pinned host key required")
		}
		return nil
	}
	if d.Kind != "s3" && d.Kind != "webdav" {
		return errors.New("unsupported destination")
	}
	u, e := url.Parse(d.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("invalid destination URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && d.AllowHTTP) {
		return errors.New("HTTPS required unless HTTP explicitly enabled")
	}
	if d.Kind == "s3" && (d.Bucket == "" || d.Username == "" || (u.Path != "" && u.Path != "/")) {
		return errors.New("S3 bucket and access key required")
	}
	return nil
}

type remoteStore interface {
	Publish(context.Context, string, string) error
	Put(context.Context, string, io.Reader, int64) error
	Open(context.Context, string) (io.ReadCloser, error)
	Remove(context.Context, string) error
	Close() error
}

func RemoveRemote(ctx context.Context, d Destination, key string) error {
	if key == "" || key == "." || path.Base(key) != key || strings.ContainsAny(key, "\\\x00") {
		return errors.New("invalid owned remote key")
	}
	r, e := openRemote(ctx, d)
	if e != nil {
		return e
	}
	defer r.Close()
	return r.Remove(ctx, key)
}

func openRemote(ctx context.Context, d Destination) (remoteStore, error) {
	if e := ValidateDestination(d); e != nil {
		return nil, e
	}
	if d.Kind == "sftp" {
		auth := ssh.Password(d.Secret)
		if strings.HasPrefix(strings.TrimSpace(d.Secret), "-----BEGIN") {
			key, e := ssh.ParsePrivateKey([]byte(d.Secret))
			if e != nil {
				return nil, errors.New("invalid SFTP private key")
			}
			auth = ssh.PublicKeys(key)
		}
		conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", d.Endpoint)
		if e != nil {
			return nil, errors.New("SFTP connection failed")
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		conn.SetDeadline(time.Now().Add(15 * time.Second))
		c, ch, r, e := ssh.NewClientConn(conn, d.Endpoint, &ssh.ClientConfig{User: d.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if ssh.FingerprintSHA256(k) != d.HostKey {
				return errors.New("SFTP host key mismatch")
			}
			return nil
		}})
		if e != nil {
			stop()
			conn.Close()
			return nil, errors.New("SFTP authentication or host key failed")
		}
		conn.SetDeadline(time.Time{})
		client := ssh.NewClient(c, ch, r)
		fs, e := sftp.NewClient(client)
		if e != nil {
			stop()
			client.Close()
			return nil, e
		}
		return &sftpStore{fs, client, stop, d.Prefix}, nil
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if d.CA != "" && !roots.AppendCertsFromPEM([]byte(d.CA)) {
		return nil, errors.New("invalid destination CA")
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	if d.Kind == "s3" {
		u, _ := url.Parse(d.Endpoint)
		region := d.Region
		if region == "" {
			region = "us-east-1"
		}
		client, e := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(d.Username, d.Secret, ""), Secure: u.Scheme == "https", Region: region, Transport: transport, BucketLookup: minio.BucketLookupPath})
		if e != nil {
			transport.CloseIdleConnections()
			return nil, e
		}
		return &s3Store{client, d.Bucket, strings.Trim(d.Prefix, "/"), transport}, nil
	}
	u, _ := url.Parse(d.Endpoint)
	u.Path = path.Join(u.Path, d.Prefix) + "/"
	return &davStore{&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, u, d.Username, d.Secret, transport}, nil
}

// Keys are generated by the caller for a single attempt, never supplied by a
// browser or reused across backups. Only encrypted archives reach destinations.
func CopyRemote(ctx context.Context, d Destination, local, key, expected string) (int64, error) {
	if path.Base(key) != key || key == "." || strings.ContainsAny(key, "\\\x00") {
		return 0, errors.New("invalid copy key")
	}
	f, e := os.Open(local)
	if e != nil {
		return 0, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return 0, e
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("invalid backup file")
	}
	remote, e := openRemote(ctx, d)
	if e != nil {
		return 0, e
	}
	defer remote.Close()
	attempt := key + ".partial"
	if e = remote.Put(ctx, attempt, f, info.Size()); e != nil {
		return 0, errors.New("remote backup upload failed")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = remote.Remove(c, attempt)
	}()
	verify := func(name string) error {
		r, e := remote.Open(ctx, name)
		if e != nil {
			return errors.New("remote backup verification unavailable")
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(r, info.Size()+1))
		r.Close()
		if e != nil || n != info.Size() || hex.EncodeToString(h.Sum(nil)) != expected {
			return errors.New("remote backup digest mismatch")
		}
		return nil
	}
	if e = verify(attempt); e != nil {
		return 0, e
	}
	if e = remote.Publish(ctx, attempt, key); e != nil {
		return 0, errors.New("remote backup publication failed")
	}
	if e = verify(key); e != nil {
		return 0, e
	}
	return info.Size(), nil
}

type sftpStore struct {
	fs     *sftp.Client
	client *ssh.Client
	stop   func() bool
	prefix string
}

func (s *sftpStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if e := s.fs.MkdirAll(s.prefix); e != nil {
		return e
	}
	f, e := s.fs.OpenFile(path.Join(s.prefix, key), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return e
	}
	n, e := io.Copy(f, io.LimitReader(r, size+1))
	ce := f.Close()
	if e != nil {
		return e
	}
	if n != size {
		return io.ErrUnexpectedEOF
	}
	return ce
}
func (s *sftpStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.fs.Open(path.Join(s.prefix, key))
}
func (s *sftpStore) Remove(ctx context.Context, key string) error {
	e := s.fs.Remove(path.Join(s.prefix, key))
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (s *sftpStore) Close() error { s.stop(); s.fs.Close(); return s.client.Close() }
func (s *sftpStore) Publish(ctx context.Context, from, to string) error {
	// Some servers implement even SFTP v3 Rename with replacement semantics.
	// OpenSSH hardlink publishes atomically without replacing an existing name.
	return s.fs.Link(path.Join(s.prefix, from), path.Join(s.prefix, to))
}

type s3Store struct {
	c              *minio.Client
	bucket, prefix string
	t              *http.Transport
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	o := minio.PutObjectOptions{ContentType: "application/octet-stream", PartSize: 16 << 20, NumThreads: 1}
	o.SetMatchETagExcept("*")
	_, e := s.c.PutObject(ctx, s.bucket, path.Join(s.prefix, key), r, size, o)
	return e
}
func (s *s3Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	o, e := s.c.GetObject(ctx, s.bucket, path.Join(s.prefix, key), minio.GetObjectOptions{})
	if e != nil {
		return nil, e
	}
	if _, e = o.Stat(); e != nil {
		o.Close()
		return nil, e
	}
	return o, nil
}
func (s *s3Store) Remove(ctx context.Context, key string) error {
	return s.c.RemoveObject(ctx, s.bucket, path.Join(s.prefix, key), minio.RemoveObjectOptions{})
}
func (s *s3Store) Close() error { s.t.CloseIdleConnections(); return nil }
func (s *s3Store) Publish(ctx context.Context, from, to string) error {
	_, e := s.c.StatObject(ctx, s.bucket, path.Join(s.prefix, to), minio.StatObjectOptions{})
	if e == nil {
		return errors.New("remote backup already exists")
	}
	code := minio.ToErrorResponse(e).Code
	if code != "NoSuchKey" && code != "NotFound" {
		return e
	}
	source, e := s.c.StatObject(ctx, s.bucket, path.Join(s.prefix, from), minio.StatObjectOptions{})
	if e != nil {
		return e
	}
	dst := minio.CopyDestOptions{Bucket: s.bucket, Object: path.Join(s.prefix, to)}
	src := minio.CopySrcOptions{Bucket: s.bucket, Object: path.Join(s.prefix, from), MatchETag: source.ETag}
	if source.Size <= 5<<30 {
		_, e = s.c.CopyObject(ctx, dst, src)
	} else {
		_, e = s.c.ComposeObject(ctx, dst, src)
	}
	return e
}

type davStore struct {
	client       *http.Client
	root         *url.URL
	user, secret string
	t            *http.Transport
}

func (s *davStore) request(ctx context.Context, method, key string, r io.Reader, size int64) (*http.Response, error) {
	u := *s.root
	u.Path = path.Join(u.Path, key)
	req, e := http.NewRequestWithContext(ctx, method, u.String(), r)
	if e != nil {
		return nil, e
	}
	req.ContentLength = size
	if s.user != "" {
		req.SetBasicAuth(s.user, s.secret)
	}
	if method == "PUT" {
		req.Header.Set("If-None-Match", "*")
	}
	return s.client.Do(req)
}
func (s *davStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	// The configured WebDAV collection must exist; never create arbitrary parents.
	resp, e := s.request(ctx, "PUT", key, r, size)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		return errors.New("WebDAV PUT rejected")
	}
	return nil
}
func (s *davStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	r, e := s.request(ctx, "GET", key, nil, 0)
	if e != nil {
		return nil, e
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, errors.New("WebDAV GET rejected")
	}
	return r.Body, nil
}
func (s *davStore) Remove(ctx context.Context, key string) error {
	r, e := s.request(ctx, "DELETE", key, nil, 0)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 204 && r.StatusCode != 404 {
		return errors.New("WebDAV DELETE rejected")
	}
	return nil
}
func (s *davStore) Close() error { s.t.CloseIdleConnections(); return nil }
func (s *davStore) Publish(ctx context.Context, from, to string) error {
	u := *s.root
	u.Path = path.Join(u.Path, from)
	dest := *s.root
	dest.Path = path.Join(dest.Path, to)
	req, e := http.NewRequestWithContext(ctx, "MOVE", u.String(), nil)
	if e != nil {
		return e
	}
	req.Header.Set("Destination", dest.String())
	req.Header.Set("Overwrite", "F")
	if s.user != "" {
		req.SetBasicAuth(s.user, s.secret)
	}
	r, e := s.client.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 201 {
		return errors.New("WebDAV MOVE rejected")
	}
	return nil
}
