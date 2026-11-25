package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const maxAgentMessageBytes = 48 << 20

var validAgentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

type Manager struct {
	tokens   map[string]string
	log      *slog.Logger
	reserved map[string]bool

	mu       sync.RWMutex
	hosts    map[string]*host
	changed  func([]Info)
	closed   bool
	closeErr error
}

func NewManager(tokens map[string]string, log *slog.Logger, changed func([]Info)) *Manager {
	if log == nil {
		log = slog.Default()
	}
	cleanTokens := make(map[string]string, len(tokens))
	for name, token := range tokens {
		if validAgentName.MatchString(name) && name != "local" && strings.TrimSpace(token) != "" {
			cleanTokens[name] = strings.TrimSpace(token)
		}
	}
	return &Manager{tokens: cleanTokens, log: log, hosts: map[string]*host{}, reserved: map[string]bool{}, changed: changed}
}

func (m *Manager) ReserveNames(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range names {
		m.reserved[name] = true
	}
}

// Enabled reports whether at least one per-device Agent pairing token is configured.
func (m *Manager) Enabled() bool {
	return len(m.tokens) > 0
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != endpointPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "WebSocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	name := strings.TrimSpace(r.Header.Get("X-Omni-Agent-Name"))
	if !validAgentName.MatchString(name) || name == "local" {
		http.Error(w, "valid X-Omni-Agent-Name header required", http.StatusBadRequest)
		return
	}
	token := m.tokens[name]
	if token == "" {
		http.Error(w, "Agent pairing token is not configured", http.StatusServiceUnavailable)
		return
	}
	if !authorized(r.Header.Get("Authorization"), token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxAgentMessageBytes)
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	info, err := readHello(ctx, conn)
	if err != nil {
		m.log.Warn("Agent 握手失败", "name", name, "err", err)
		_ = writeJSON(ctx, conn, &sync.Mutex{}, message{Type: "reject", Error: err.Error()})
		return
	}
	current := &host{
		info:    info,
		conn:    conn,
		send:    make(chan message, 32),
		closed:  make(chan struct{}),
		pending: map[string]chan message{},
	}
	if info.Name != name {
		err := errors.New("Agent name does not match its authenticated identity")
		m.log.Warn("Agent 注册失败", "name", info.Name, "err", err)
		_ = writeJSON(ctx, conn, &current.writes, message{Type: "reject", Error: err.Error()})
		return
	}
	if err := m.register(current); err != nil {
		m.log.Warn("Agent 注册失败", "name", info.Name, "err", err)
		_ = writeJSON(ctx, conn, &current.writes, message{Type: "reject", Error: err.Error()})
		return
	}
	defer m.unregister(current)
	if err := writeJSON(ctx, conn, &current.writes, message{Type: "ready"}); err != nil {
		return
	}
	go m.writeLoop(ctx, current)
	m.log.Info("Agent 已连接", "name", info.Name, "os", info.OS, "workdir", info.Workdir, "exec_enabled", info.AllowExec)

	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				m.log.Info("Agent 已断开", "name", info.Name, "err", err)
			}
			return
		}
		var incoming message
		if err := json.Unmarshal(payload, &incoming); err != nil {
			m.log.Warn("Agent 消息无效", "name", info.Name, "err", err)
			return
		}
		switch incoming.Type {
		case "heartbeat":
		case "response":
			current.pendingMu.Lock()
			pending := current.pending[incoming.ID]
			if pending != nil {
				delete(current.pending, incoming.ID)
			}
			current.pendingMu.Unlock()
			if pending != nil {
				select {
				case pending <- incoming:
				default:
				}
			}
		default:
			m.log.Warn("Agent 收到未知消息类型", "name", info.Name, "type", incoming.Type)
			return
		}
	}
}

func readHello(ctx context.Context, conn *websocket.Conn) (Info, error) {
	_, payload, err := conn.Read(ctx)
	if err != nil {
		return Info{}, fmt.Errorf("read hello: %w", err)
	}
	var incoming message
	if err := json.Unmarshal(payload, &incoming); err != nil {
		return Info{}, fmt.Errorf("decode hello: %w", err)
	}
	info := Info{Name: incoming.Name, Version: incoming.Version, OS: incoming.OS, Shell: incoming.Shell, Workdir: incoming.Workdir, AllowExec: incoming.AllowExec}
	if incoming.Type != "hello" {
		return Info{}, fmt.Errorf("the first Agent message must be hello")
	}
	if !validAgentName.MatchString(info.Name) || info.Name == "local" {
		return Info{}, fmt.Errorf("agent name must be a unique lowercase name using letters, numbers, and hyphens")
	}
	if info.OS != "windows" && info.OS != "linux" && info.OS != "darwin" {
		return Info{}, fmt.Errorf("unsupported Agent operating system %q", info.OS)
	}
	if info.OS == "windows" {
		if !windowsAbsolutePath.MatchString(info.Workdir) {
			return Info{}, fmt.Errorf("Windows Agent must register an absolute Windows working directory")
		}
	} else if !filepath.IsAbs(info.Workdir) {
		return Info{}, fmt.Errorf("Agent must register an absolute working directory")
	}
	if strings.ContainsAny(info.Workdir, "\r\n\x00") {
		return Info{}, fmt.Errorf("Agent working directory contains invalid characters")
	}
	return info, nil
}

var windowsAbsolutePath = regexp.MustCompile(`(?i)^(?:[a-z]:[\\/]|\\\\[^\\/]+[\\/][^\\/]+[\\/])`)

func (m *Manager) register(current *host) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("the Omni MCP Hub is stopping")
	}
	if m.reserved[current.info.Name] {
		m.mu.Unlock()
		return fmt.Errorf("Agent name %q is already configured as a static SSH target", current.info.Name)
	}
	if m.hosts[current.info.Name] != nil {
		m.mu.Unlock()
		return fmt.Errorf("Agent name %q is already connected; choose a different --name", current.info.Name)
	}
	m.hosts[current.info.Name] = current
	callback := m.changed
	infos := m.infosLocked()
	m.mu.Unlock()
	if callback != nil {
		callback(infos)
	}
	return nil
}

func (m *Manager) unregister(current *host) {
	m.mu.Lock()
	if m.hosts[current.info.Name] == current {
		delete(m.hosts, current.info.Name)
	}
	callback := m.changed
	infos := m.infosLocked()
	m.mu.Unlock()
	closeHost(current, "Agent connection closed")
	if callback != nil {
		callback(infos)
	}
}

func (m *Manager) infosLocked() []Info {
	infos := make([]Info, 0, len(m.hosts))
	for _, current := range m.hosts {
		infos = append(infos, current.info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

func (m *Manager) Infos() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.infosLocked()
}

func (m *Manager) Call(ctx context.Context, name, method string, params any, result any) error {
	m.mu.RLock()
	current := m.hosts[name]
	m.mu.RUnlock()
	if current == nil {
		return fmt.Errorf("Agent %q is offline", name)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode Agent request: %w", err)
	}
	id := randomID()
	response := make(chan message, 1)
	current.pendingMu.Lock()
	current.pending[id] = response
	current.pendingMu.Unlock()
	defer func() {
		current.pendingMu.Lock()
		delete(current.pending, id)
		current.pendingMu.Unlock()
	}()
	request := message{Type: "request", ID: id, Method: method, Params: encoded}
	if deadline, ok := ctx.Deadline(); ok {
		request.Deadline = deadline.UnixMilli()
	}
	select {
	case current.send <- request:
	case <-ctx.Done():
		select {
		case current.send <- message{Type: "cancel", ID: id}:
		default:
		}
		return ctx.Err()
	case <-current.closed:
		return fmt.Errorf("Agent %q disconnected before accepting the request", name)
	}
	select {
	case reply := <-response:
		if reply.Error != "" {
			return errors.New(reply.Error)
		}
		if result == nil {
			return nil
		}
		if len(reply.Result) == 0 {
			return errors.New("Agent returned an empty result")
		}
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("decode Agent response: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-current.closed:
		return fmt.Errorf("Agent %q disconnected while handling the request", name)
	}
}

func (m *Manager) writeLoop(ctx context.Context, current *host) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-current.closed:
			return
		case outgoing := <-current.send:
			err := writeJSON(ctx, current.conn, &current.writes, outgoing)
			if err != nil {
				m.log.Warn("发送任务到 Agent 失败", "name", current.info.Name, "err", err)
				return
			}
		}
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	connections := make([]*host, 0, len(m.hosts))
	for _, current := range m.hosts {
		connections = append(connections, current)
	}
	m.hosts = map[string]*host{}
	callback := m.changed
	m.mu.Unlock()
	for _, current := range connections {
		closeHost(current, "Hub shutting down")
	}
	if callback != nil {
		callback(nil)
	}
}

func closeHost(current *host, reason string) {
	if current.closedOnce.CompareAndSwap(false, true) {
		close(current.closed)
		_ = current.conn.CloseNow()
	}
}

func writeJSON(ctx context.Context, conn *websocket.Conn, gate *sync.Mutex, value message) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	gate.Lock()
	defer gate.Unlock()
	return conn.Write(ctx, websocket.MessageText, encoded)
}

func authorized(header, token string) bool {
	provided := strings.TrimPrefix(header, "Bearer ")
	if provided == header || len(provided) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func randomID() string {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		panic("agent: cryptographic random source unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
