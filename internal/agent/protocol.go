package agent

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

const endpointPath = "/agent/ws"

type message struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Version   string          `json:"version,omitempty"`
	OS        string          `json:"os,omitempty"`
	Shell     string          `json:"shell,omitempty"`
	Workdir   string          `json:"workdir,omitempty"`
	AllowExec bool            `json:"allow_exec,omitempty"`
	Deadline  int64           `json:"deadline,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type Info struct {
	Name      string
	Version   string
	OS        string
	Shell     string
	Workdir   string
	AllowExec bool
}

type host struct {
	info       Info
	conn       *websocket.Conn
	send       chan message
	closed     chan struct{}
	writes     sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan message
	closedOnce atomic.Bool
}
