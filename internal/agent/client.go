package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

type ClientOptions struct {
	Hub       string
	Token     string
	TokenEnv  string
	Name      string
	Version   string
	AllowDirs []string
	AllowExec bool
	NoPrompt  bool
	OnEvent   func(Event)
	Log       *slog.Logger
}

// Event describes an observable Agent lifecycle or tool operation event for UI/logging consumers.
type Event struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"` // connecting | connected | disconnected | operation
	Method     string    `json:"method,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	Error      string    `json:"error,omitempty"`
	RetryInSec int       `json:"retry_in_sec,omitempty"`
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if !validAgentName.MatchString(name) || name == "local" {
		return fmt.Errorf("agent name must use lowercase letters, numbers, and hyphens, and cannot be local")
	}
	return nil
}

func ValidateHubURL(raw string) error {
	_, err := agentEndpoint(raw)
	return err
}

func DetectedShell() string {
	return agentShellName(runtime.GOOS)
}

func (c *Client) emit(ev Event) {
	if c.opts.OnEvent == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	c.opts.OnEvent(ev)
}

type Client struct {
	opts  ClientOptions
	url   string
	roots []*os.Root
	log   *slog.Logger
	mu    sync.Mutex
	procs map[string]*agentProcess
}

func NewClient(options ClientOptions) (*Client, error) {
	if !validAgentName.MatchString(options.Name) || options.Name == "local" {
		return nil, fmt.Errorf("agent name must use lowercase letters, numbers, and hyphens, and cannot be local")
	}
	if len(options.AllowDirs) == 0 {
		return nil, fmt.Errorf("at least one --allow-dir is required")
	}
	if options.TokenEnv == "" {
		options.TokenEnv, _ = TokenEnvName(options.Name)
	}
	if options.Token == "" {
		options.Token = strings.TrimSpace(os.Getenv(options.TokenEnv))
	}
	if options.Token == "" && !options.NoPrompt && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "Omni MCP Hub 配对令牌（输入不回显）: ")
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err == nil {
			options.Token = strings.TrimSpace(string(secret))
		}
	}
	if options.Token == "" {
		return nil, fmt.Errorf("set %s or start Agent from an interactive terminal to enter the pairing token", options.TokenEnv)
	}
	endpoint, err := agentEndpoint(options.Hub)
	if err != nil {
		return nil, err
	}
	if options.Log == nil {
		options.Log = slog.Default()
	}
	if options.Version == "" {
		options.Version = "dev"
	}
	client := &Client{opts: options, url: endpoint, log: options.Log, procs: map[string]*agentProcess{}}
	for _, raw := range options.AllowDirs {
		path, err := filepath.Abs(strings.TrimSpace(raw))
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("resolve allowed directory %q: %w", raw, err)
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("open allowed directory %q: %w", raw, err)
		}
		client.roots = append(client.roots, root)
	}
	return client, nil
}

func agentEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("--hub must be an absolute https:// URL")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("--hub accepts only an HTTPS origin; its Agent endpoint is /agent/ws")
	}
	if parsed.Scheme == "http" {
		host := strings.ToLower(parsed.Hostname())
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return "", fmt.Errorf("--hub must use HTTPS except for loopback development")
		}
	}
	parsed.Path = endpointPath
	parsed.RawPath = ""
	return parsed.String(), nil
}

func (c *Client) Close() {
	c.cleanupProcesses()
	for _, root := range c.roots {
		_ = root.Close()
	}
	c.roots = nil
}

func (c *Client) Run(ctx context.Context) error {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sessionStart := time.Now()
		err := c.runSession(ctx)
		delay = retryDelayAfterSession(delay, time.Since(sessionStart))
		if err != nil && !errors.Is(err, context.Canceled) {
			c.log.Warn("Hub 连接中断，将自动重连", "name", c.opts.Name, "err", err, "retry_in", delay)
			c.emit(Event{Kind: "disconnected", Error: err.Error(), RetryInSec: int(delay / time.Second)})
		}
		if ctx.Err() != nil {
			c.emit(Event{Kind: "disconnected"})
			return ctx.Err()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = nextRetryDelay(delay)
	}
}

func retryDelayAfterSession(delay, sessionDuration time.Duration) time.Duration {
	if sessionDuration >= 30*time.Second {
		return time.Second
	}
	return delay
}

func nextRetryDelay(delay time.Duration) time.Duration {
	if delay >= 30*time.Second {
		return 30 * time.Second
	}
	delay *= 2
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func (c *Client) runSession(ctx context.Context) error {
	c.emit(Event{Kind: "connecting", Summary: c.opts.Hub})
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.opts.Token)
	header.Set("X-Omni-Agent-Name", c.opts.Name)
	conn, _, err := websocket.Dial(ctx, c.url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return fmt.Errorf("connect to Hub: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "agent reconnecting")
	sessionCtx, cancelSession := context.WithCancel(ctx)
	defer func() {
		cancelSession()
		c.cleanupProcesses()
	}()
	conn.SetReadLimit(maxAgentMessageBytes)
	root := c.roots[0].Name()
	if err := c.write(sessionCtx, conn, message{Type: "hello", Name: c.opts.Name, Version: c.opts.Version, OS: runtime.GOOS, Shell: agentShellName(runtime.GOOS), Workdir: root, AllowExec: c.opts.AllowExec}); err != nil {
		return err
	}
	_, payload, err := conn.Read(sessionCtx)
	if err != nil {
		return fmt.Errorf("read Hub registration: %w", err)
	}
	var ready message
	if err := json.Unmarshal(payload, &ready); err != nil {
		return err
	}
	if ready.Type != "ready" {
		if ready.Error != "" {
			return errors.New(ready.Error)
		}
		return fmt.Errorf("Hub rejected Agent registration")
	}
	c.log.Info("已连接 Omni MCP Hub", "name", c.opts.Name, "allowed_dirs", len(c.roots), "exec_enabled", c.opts.AllowExec)
	c.emit(Event{Kind: "connected", Summary: root})
	var writes sync.Mutex
	semaphore := make(chan struct{}, 4)
	var inflight sync.Map
	readErr := make(chan error, 1)
	go func() {
		for {
			_, payload, err := conn.Read(sessionCtx)
			if err != nil {
				readErr <- err
				return
			}
			var request message
			if err := json.Unmarshal(payload, &request); err != nil {
				readErr <- fmt.Errorf("decode Hub request: %w", err)
				return
			}
			if request.Type == "cancel" {
				if cancel, ok := inflight.Load(request.ID); ok {
					cancel.(context.CancelFunc)()
				}
				continue
			}
			if request.Type != "request" {
				continue
			}
			select {
			case semaphore <- struct{}{}:
				go func(request message) {
					defer func() { <-semaphore }()
					var requestCtx context.Context
					var cancel context.CancelFunc
					if request.Deadline > 0 {
						if deadline := time.UnixMilli(request.Deadline); deadline.After(time.Now()) {
							requestCtx, cancel = context.WithDeadline(sessionCtx, deadline)
						} else {
							requestCtx, cancel = context.WithCancel(sessionCtx)
							cancel()
						}
					} else {
						requestCtx, cancel = context.WithCancel(sessionCtx)
					}
					inflight.Store(request.ID, cancel)
					defer func() {
						inflight.Delete(request.ID)
						cancel()
					}()
					response := c.handle(requestCtx, request)
					if err := c.write(sessionCtx, conn, response, &writes); err != nil {
						conn.Close(websocket.StatusInternalError, "Agent response failed")
					}
				}(request)
			case <-sessionCtx.Done():
				readErr <- sessionCtx.Err()
				return
			}
		}
	}()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-sessionCtx.Done():
			return sessionCtx.Err()
		case err := <-readErr:
			return err
		case <-ticker.C:
			if err := c.write(sessionCtx, conn, message{Type: "heartbeat"}, &writes); err != nil {
				return err
			}
		}
	}
}

func (c *Client) write(ctx context.Context, conn *websocket.Conn, outgoing message, gate ...*sync.Mutex) error {
	data, err := json.Marshal(outgoing)
	if err != nil {
		return err
	}
	if len(gate) > 0 {
		gate[0].Lock()
		defer gate[0].Unlock()
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
