package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/agentupdate"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"time"
)

var ErrUpdate = errors.New("drained for update")
var ErrRotate = errors.New("device key rotation requested")

type Config struct {
	HeartbeatSeconds         int                 `json:"heartbeatSeconds,omitempty"`
	BasicUsername            string              `json:"basicUsername,omitempty"`
	BasicPassword            string              `json:"basicPassword,omitempty"`
	Managed                  bool                `json:"-"`
	Updates                  *agentupdate.Config `json:"updates,omitempty"`
	SchemaVersion            int                 `json:"schemaVersion,omitempty"`
	ProxyURL                 string              `json:"proxyUrl,omitempty"`
	Headers                  map[string]string   `json:"headers,omitempty"`
	CAFile                   string              `json:"caFile,omitempty"`
	CertFile                 string              `json:"certFile,omitempty"`
	KeyFile                  string              `json:"keyFile,omitempty"`
	PendingKey               string              `json:"pendingKey,omitempty"`
	Version                  string              `json:"-"`
	CollectionSeconds        int                 `json:"collectionSeconds,omitempty"`
	NetworkPattern           string              `json:"networkPattern,omitempty"`
	DiskPaths                []string            `json:"diskPaths,omitempty"`
	Endpoint                 string              `json:"endpoint"`
	AgentID                  string              `json:"agentId"`
	DeviceKey                string              `json:"deviceKey"`
	AllowInsecureDevelopment bool                `json:"allowInsecureDevelopment"`
}

func (c Config) URL() (string, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("endpoint must be an HTTPS origin")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		if !c.AllowInsecureDevelopment {
			return "", errors.New("HTTP requires explicit development opt-in")
		}
		u.Scheme = "ws"
	default:
		return "", errors.New("unsupported endpoint scheme")
	}
	u.Path = "/api/agent/v1/connect"
	return u.String(), nil
}
func HTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
}

// RunConnection never resumes data streams after reconnect. Ready means only
// that the authenticated control handshake has completed.
func RunConnection(ctx context.Context, c Config, ready func(int64)) error {
	endpoint, e := c.URL()
	if e != nil {
		return e
	}
	if len(c.DeviceKey) != 64 {
		return errors.New("invalid device credential")
	}
	client, e := c.Client()
	if e != nil {
		return e
	}
	handshake, done := context.WithTimeout(ctx, 10*time.Second)
	headers := http.Header{}
	DeviceAuth(headers, c)
	ws, res, e := websocket.Dial(handshake, endpoint, &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{agentwire.Protocol}, HTTPClient: client})
	done()
	if e != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		return errors.New("agent connection rejected or unavailable")
	}
	defer ws.CloseNow()
	if ws.Subprotocol() != agentwire.Protocol {
		return agentwire.ErrProtocol
	}
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	ws.SetReadLimit(512 * 1024)
	mux, e := yamux.Client(conn, agentwire.MuxConfig())
	if e != nil {
		return e
	}
	defer mux.Close()
	stop := context.AfterFunc(ctx, func() { _ = mux.Close(); _ = ws.CloseNow() })
	defer stop()
	control, e := mux.OpenStream()
	if e != nil {
		return e
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(10 * time.Second))
	caps := []string{"heartbeat", "dial.v1", "metrics.v1", "key-rotation.v1"}
	if c.Managed && c.Updates != nil {
		caps = append(caps, "managed-update.v1")
	}
	helloPayload, _ := json.Marshal(map[string]any{"capabilities": caps, "version": c.Version, "heartbeatSeconds": c.HeartbeatSeconds})
	if e = agentwire.WriteJSON(control, agentwire.Control{V: 1, Type: "hello", ID: "hello", Payload: helloPayload}); e != nil {
		return e
	}
	var ack agentwire.Control
	if e = agentwire.ReadJSON(control, &ack); e != nil {
		return e
	}
	if ack.V != 1 || ack.Type != "hello_ack" || ack.ID != "hello" || ack.Epoch <= 0 {
		return agentwire.ErrProtocol
	}
	var hello struct {
		AgentID          string                    `json:"agentId"`
		Scope            connectivity.NetworkScope `json:"networkScope"`
		HeartbeatSeconds int                       `json:"heartbeatSeconds"`
	}
	if e = json.Unmarshal(ack.Payload, &hello); e != nil || hello.AgentID != c.AgentID || (hello.HeartbeatSeconds < 10 || hello.HeartbeatSeconds > 60) {
		return agentwire.ErrProtocol
	}
	if ready != nil {
		ready(ack.Epoch)
	}
	c.HeartbeatSeconds = hello.HeartbeatSeconds
	return controlLoop(ctx, mux, control, ack.Epoch, hello.Scope, c)
}
