package connectivity

import (
	"bytes"
	"context"
	"golang.org/x/crypto/ssh"
	"net"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"sync"
)

type Jump struct {
	ParentID     string `json:"parentId"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	CredentialID string `json:"credentialId"`
	Enabled      bool   `json:"enabled"`
	Version      int64  `json:"version"`
	PublicKey    string `json:"publicKey"`
	Fingerprint  string `json:"fingerprint"`
}

const jumpColumns = `id,name,host,port,credential_id,enabled,version,public_key,fingerprint,COALESCE(parent_id::text,'')`

func (s *Service) Jump(ctx context.Context, id string) (v Jump, e error) {
	if !resource.ValidID(id) {
		return v, fault.Invalid
	}
	e = s.db.Row(ctx, `SELECT `+jumpColumns+` FROM ssh_jump_hosts WHERE id=$1`, id).Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.CredentialID, &v.Enabled, &v.Version, &v.PublicKey, &v.Fingerprint, &v.ParentID)
	return
}
func (s *Service) Jumps(ctx context.Context) ([]Jump, error) {
	rows, e := s.db.Rows(ctx, `SELECT `+jumpColumns+` FROM ssh_jump_hosts ORDER BY name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Jump{}
	for rows.Next() {
		var v Jump
		if e = rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.CredentialID, &v.Enabled, &v.Version, &v.PublicKey, &v.Fingerprint, &v.ParentID); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Trust is changed only through TrustJump, never as a side effect of editing an endpoint.
func (s *Service) SaveJump(ctx context.Context, v Jump) (string, error) {
	if e := resource.Validate(resource.Asset{Name: v.Name, Host: v.Host, Port: v.Port, Protocol: "ssh", CredentialID: v.CredentialID, ConnectTimeout: 10}); e != nil {
		return "", e
	}
	if e := s.validateJumpGraph(ctx, v); e != nil {
		return "", e
	}
	if v.ID == "" {
		e := s.db.Row(ctx, `INSERT INTO ssh_jump_hosts(name,host,port,credential_id,enabled,parent_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid) RETURNING id`, v.Name, v.Host, v.Port, v.CredentialID, v.Enabled, v.ParentID).Scan(&v.ID)
		return v.ID, e
	}
	if !resource.ValidID(v.ID) {
		return "", fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE ssh_jump_hosts SET name=$2,host=$3,port=$4,credential_id=$5,enabled=$6,parent_id=NULLIF($8,'')::uuid,version=version+1,public_key=CASE WHEN host=$3 AND port=$4 AND COALESCE(parent_id::text,'')=$8 THEN public_key ELSE '' END,fingerprint=CASE WHEN host=$3 AND port=$4 AND COALESCE(parent_id::text,'')=$8 THEN fingerprint ELSE '' END WHERE id=$1 AND version=$7`, v.ID, v.Name, v.Host, v.Port, v.CredentialID, v.Enabled, v.Version, v.ParentID)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}
func (s *Service) TrustJump(ctx context.Context, id string, version int64, publicKey string) error {
	key, _, _, rest, e := ssh.ParseAuthorizedKey([]byte(publicKey))
	if e != nil || len(bytes.TrimSpace(rest)) != 0 {
		return fault.Invalid
	}
	if _, ok := key.(*ssh.Certificate); ok {
		return fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE ssh_jump_hosts SET public_key=$3,fingerprint=$4,version=version+1 WHERE id=$1 AND version=$2`, id, version, string(ssh.MarshalAuthorizedKey(key)), ssh.FingerprintSHA256(key))
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}

type jumpConn struct {
	net.Conn
	client *ssh.Client
	once   sync.Once
	stop   func() bool
}

func (c *jumpConn) Close() error {
	var e error
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
		e = c.Conn.Close()
		_ = c.client.Close()
	})
	return e
}

// One owned SSH connection per target stream. No fallback to a direct target dial.
func DialJump(life, ctx context.Context, v Jump, username string, auth ssh.AuthMethod, host string, port int) (net.Conn, error) {
	return DialJumpUsing(life, ctx, v, username, auth, host, port, nil)
}
func DialJumpUsing(life, ctx context.Context, v Jump, username string, auth ssh.AuthMethod, host string, port int, transport Dialer) (net.Conn, error) {
	if !v.Enabled || v.PublicKey == "" {
		return nil, fault.Denied
	}
	client, e := ConnectUsing(ctx, Trust{Host: v.Host, Port: v.Port, PublicKey: v.PublicKey}, username, auth, transport)
	if e != nil {
		return nil, e
	}
	stop := context.AfterFunc(life, func() { _ = client.Close() })
	conn, e := client.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if e != nil {
		stop()
		_ = client.Close()
		return nil, e
	}
	return &jumpConn{Conn: conn, client: client, stop: stop}, nil
}

// Called inside the serialized management mutation transaction. Check descendants too:
// reparenting an ancestor must not silently make existing chains exceed four hops.
func (s *Service) validateJumpGraph(ctx context.Context, v Jump) error {
	if v.ParentID != "" && !resource.ValidID(v.ParentID) {
		return fault.Invalid
	}
	rows, e := s.Jumps(ctx)
	if e != nil {
		return e
	}
	parents := map[string]string{}
	for _, j := range rows {
		parents[j.ID] = j.ParentID
	}
	id := v.ID
	if id == "" {
		id = "new"
	}
	parents[id] = v.ParentID
	for start := range parents {
		seen := map[string]bool{}
		depth := 0
		for n := start; n != ""; {
			if seen[n] || depth >= 4 {
				return fault.Invalid
			}
			seen[n] = true
			depth++
			parent, ok := parents[n]
			if !ok {
				return fault.Invalid
			}
			n = parent
		}
	}
	return nil
}
