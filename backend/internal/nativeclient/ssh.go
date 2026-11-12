package nativeclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	ws "github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"strings"
	"time"
)

// ConnectSSH keeps both OAuth and per-connection SSH private keys in memory.
// The remote server key is pinned to the reservation delivered over verified TLS.
func (c *Client) ConnectSSH(ctx context.Context, token Token, asset string) (*ssh.Client, error) {
	return c.connectSSH(ctx, token, asset, "ssh")
}
func (c *Client) ConnectForward(ctx context.Context, token Token, asset string) (*ssh.Client, error) {
	return c.connectSSH(ctx, token, asset, "ssh-forward")
}
func (c *Client) connectSSH(ctx context.Context, token Token, asset, entry string) (*ssh.Client, error) {
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, e
	}
	defer clear(private)
	signer, e := ssh.NewSignerFromKey(private)
	if e != nil {
		return nil, e
	}
	body, _ := json.Marshal(map[string]string{"assetId": asset, "entry": entry, "clientVersion": "nt-client-r14-ssh", "idempotencyKey": random(), "clientPublicKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))})
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+"/api/native/v1/connect", bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+token.Access)
	req.Header.Set("Content-Type", "application/json")
	var reservation struct {
		SessionID string    `json:"sessionId"`
		Ticket    string    `json:"bridgeTicket"`
		URL       string    `json:"bridgeUrl"`
		HostKey   string    `json:"hostKey"`
		Expires   time.Time `json:"expiresAt"`
	}
	if e = c.do(req, &reservation); e != nil {
		return nil, e
	}
	if len(reservation.Ticket) != 64 || reservation.URL != "/api/native/v1/bridge" || reservation.Expires.Before(time.Now()) || reservation.Expires.After(time.Now().Add(61*time.Second)) {
		return nil, errors.New("invalid bridge reservation")
	}
	host, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(reservation.HostKey))
	if e != nil || host.Type() != ssh.KeyAlgoED25519 || len(options) != 0 || len(rest) != 0 {
		return nil, errors.New("invalid bridge host key")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+reservation.Ticket)
	socket, _, e := ws.Dial(ctx, "wss"+strings.TrimPrefix(c.origin, "https")+reservation.URL, &ws.DialOptions{HTTPClient: c.http, HTTPHeader: headers, Subprotocols: []string{"nt-bridge.v1"}, CompressionMode: ws.CompressionDisabled})
	if e != nil {
		return nil, errors.New("SSH bridge upgrade failed")
	}
	raw := &bridgeSSHConn{Conn: ws.NetConn(ctx, socket, ws.MessageBinary)}
	socket.SetReadLimit(64 << 10)
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	conn, channels, requests, e := ssh.NewClientConn(raw, "native", &ssh.ClientConfig{User: "nt", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(host)})
	if e != nil {
		socket.CloseNow()
		return nil, errors.New("SSH bridge handshake failed")
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(conn, channels, requests), nil
}

type bridgeSSHConn struct{ net.Conn }

func (c *bridgeSSHConn) Write(p []byte) (int, error) {
	if e := c.Conn.SetWriteDeadline(time.Now().Add(15 * time.Second)); e != nil {
		return 0, e
	}
	return c.Conn.Write(p)
}
