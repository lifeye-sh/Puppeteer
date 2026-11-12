package connectivity

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"net"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"strconv"
	"time"
)

var ErrHostKey = errors.New("host_key_mismatch")

type Service struct {
	db     *postgres.DB
	agents agentChannels
}

func New(db *postgres.DB) *Service {
	return &Service{db: db, agents: agentChannels{active: map[string]*agentChannel{}}}
}

type Trust struct {
	AssetID     string `json:"assetId"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
	Version     int64  `json:"version"`
}

func (s *Service) Get(ctx context.Context, id string) (v Trust, e error) {
	e = s.db.Row(ctx, `SELECT asset_id,host,port,public_key,fingerprint,version FROM ssh_known_hosts WHERE asset_id=$1`, id).Scan(&v.AssetID, &v.Host, &v.Port, &v.PublicKey, &v.Fingerprint, &v.Version)
	return
}
func (s *Service) Save(ctx context.Context, v Trust, actor string) error {
	key, _, _, rest, e := ssh.ParseAuthorizedKey([]byte(v.PublicKey))
	if e != nil || len(bytes.TrimSpace(rest)) != 0 {
		return fault.Invalid
	}
	if _, ok := key.(*ssh.Certificate); ok {
		return fault.Invalid
	}
	v.PublicKey = string(ssh.MarshalAuthorizedKey(key))
	v.Fingerprint = ssh.FingerprintSHA256(key)
	if v.Version == 0 {
		_, e = s.db.Exec(ctx, `INSERT INTO ssh_known_hosts(asset_id,host,port,public_key,fingerprint,trusted_by) VALUES($1,$2,$3,$4,$5,$6)`, v.AssetID, v.Host, v.Port, v.PublicKey, v.Fingerprint, actor)
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE ssh_known_hosts SET host=$2,port=$3,public_key=$4,fingerprint=$5,trusted_by=$6,trusted_at=now(),version=version+1 WHERE asset_id=$1 AND version=$7`, v.AssetID, v.Host, v.Port, v.PublicKey, v.Fingerprint, actor, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}

// Resolve exactly once and dial that IP, preventing DNS rebinding to metadata/link-local addresses.
func dial(ctx context.Context, host string, port int) (net.Conn, error) {
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, e
	}
	if len(ips) == 0 {
		return nil, fault.Invalid
	}
	for _, v := range ips {
		if v.IP.IsUnspecified() || v.IP.IsMulticast() || v.IP.IsLinkLocalUnicast() || v.IP.IsLinkLocalMulticast() {
			return nil, fault.Denied
		}
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ips[0].IP.String(), strconv.Itoa(port)))
}

type Dialer func(context.Context, string, int) (net.Conn, error)

// DialTCP shares the validated resolver used by direct SSH connections.
func DialTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	return dial(ctx, host, port)
}

func handshake(ctx context.Context, host string, port int, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	return handshakeUsing(ctx, host, port, cfg, nil)
}
func handshakeUsing(ctx context.Context, host string, port int, cfg *ssh.ClientConfig, transport Dialer) (*ssh.Client, error) {
	if transport == nil {
		transport = dial
	}
	conn, e := transport(ctx, host, port)
	if e != nil {
		return nil, e
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	c, ch, req, e := ssh.NewClientConn(conn, net.JoinHostPort(host, strconv.Itoa(port)), cfg)
	if e != nil {
		conn.Close()
		return nil, e
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, ch, req), nil
}

// Discovery deliberately aborts BEFORE authentication. It never establishes trust automatically.
func Discover(ctx context.Context, host string, port int) (Trust, error) {
	return DiscoverUsing(ctx, host, port, nil)
}
func DiscoverUsing(ctx context.Context, host string, port int, transport Dialer) (Trust, error) {
	var v Trust
	sentinel := errors.New("observed")
	_, e := handshakeUsing(ctx, host, port, &ssh.ClientConfig{User: "host-key-inspection", HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
		v = Trust{Host: host, Port: port, PublicKey: string(ssh.MarshalAuthorizedKey(k)), Fingerprint: ssh.FingerprintSHA256(k)}
		return sentinel
	}}, transport)
	if v.PublicKey != "" {
		return v, nil
	}
	return v, e
}
func Connect(ctx context.Context, v Trust, username string, auth ssh.AuthMethod) (*ssh.Client, error) {
	return ConnectUsing(ctx, v, username, auth, nil)
}
func ConnectUsing(ctx context.Context, v Trust, username string, auth ssh.AuthMethod, transport Dialer) (*ssh.Client, error) {
	expected, _, _, _, e := ssh.ParseAuthorizedKey([]byte(v.PublicKey))
	if e != nil {
		return nil, e
	}
	return handshakeUsing(ctx, v.Host, v.Port, &ssh.ClientConfig{User: username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !bytes.Equal(expected.Marshal(), key.Marshal()) {
			return ErrHostKey
		}
		return nil
	}}, transport)
}
