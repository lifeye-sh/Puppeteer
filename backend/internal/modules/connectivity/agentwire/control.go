package agentwire

import (
	"encoding/json"
	"github.com/hashicorp/yamux"
	"io"
	"time"
)

const Protocol = "nt-agent.v1"

type Control struct {
	V       int             `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Epoch   int64           `json:"epoch"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func MuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.AcceptBacklog = 1
	c.MaxStreamWindowSize = 256 * 1024
	c.ConnectionWriteTimeout = 15 * time.Second
	c.StreamOpenTimeout = 10 * time.Second
	c.StreamCloseTimeout = 5 * time.Second
	c.LogOutput = io.Discard
	return c
}
