package agentui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"omni-mcp/internal/agent"
)

const maxEventHistory = 200

// UIEvent is an observable entry shown in the live Agent UI audit feed.
type UIEvent struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"` // connecting | connected | disconnected | operation | system
	Method     string    `json:"method,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	Error      string    `json:"error,omitempty"`
	RetryInSec int       `json:"retry_in_sec,omitempty"`
}

// PairingState represents an active one-click Hub device pairing request.
type PairingState struct {
	Code       string    `json:"code"`
	ApproveURL string    `json:"approve_url"`
	DeviceName string    `json:"device_name"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Snapshot is the complete sanitized state rendered by the browser UI.
type Snapshot struct {
	State        string        `json:"state"` // disconnected | pairing | connecting | connected | reconnecting
	ConnectedAt  *time.Time    `json:"connected_at,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
	RetryInSec   int           `json:"retry_in_sec,omitempty"`
	Pairing      *PairingState `json:"pairing,omitempty"`
	Version      string        `json:"version"`
	OS           string        `json:"os"`
	Arch         string        `json:"arch"`
	Shell        string        `json:"shell"`
	ConfigPath   string        `json:"config_path"`
	TokenStorage string        `json:"token_storage"`
	Profile      PublicProfile `json:"profile"`
	CLIPreview   string        `json:"cli_preview"`
	Events       []UIEvent     `json:"events"`
}

// ConfigUpdate is sent by POST /api/config.
type ConfigUpdate struct {
	Hub         string   `json:"hub"`
	Name        string   `json:"name"`
	AllowDirs   []string `json:"allow_dirs"`
	AllowExec   bool     `json:"allow_exec"`
	AutoConnect bool     `json:"auto_connect"`
	SaveToken   bool     `json:"save_token"`
	Token       string   `json:"token,omitempty"`
	ConnectNow  bool     `json:"connect_now,omitempty"`
}

// Controller manages the Agent profile, lifecycle, and live SSE subscribers.
type Controller struct {
	configPath string
	version    string
	log        *slog.Logger
	httpClient *http.Client
	noOpen     bool

	mu           sync.Mutex
	profile      Profile
	runtimeToken string
	state        string
	connectedAt  *time.Time
	lastError    string
	retryInSec   int
	everConnGen  uint64
	pairing      *PairingState
	pairGen      uint64
	pairCancel   context.CancelFunc
	gen          uint64
	cancelRun    context.CancelFunc
	client       *agent.Client
	doneCh       chan struct{}
	nextEventID  int64
	events       []UIEvent
	subs         map[chan struct{}]struct{}
}

func NewController(configPath, version string, log *slog.Logger) (*Controller, error) {
	if configPath == "" {
		p, err := DefaultConfigPath()
		if err != nil {
			return nil, err
		}
		configPath = p
	}
	if log == nil {
		log = slog.Default()
	}
	if version == "" {
		version = "dev"
	}
	prof, err := loadProfile(configPath)
	if err != nil {
		return nil, err
	}
	c := &Controller{
		configPath: configPath,
		version:    version,
		log:        log,
		httpClient: &http.Client{Timeout: 12 * time.Second},
		profile:    prof,
		state:      "disconnected",
		events:     make([]UIEvent, 0, 32),
		subs:       map[chan struct{}]struct{}{},
	}
	if strings.TrimSpace(prof.TokenSealed) != "" && prof.tokenMatchesName() {
		if plain, err := unsealToken(prof.TokenSealed); err == nil && plain != "" {
			c.runtimeToken = plain
		} else if err != nil {
			c.log.Warn("读取已保存配对令牌失败，请重新输入", "err", err)
		}
	}
	return c, nil
}

// SetNoOpen disables automatic browser opening (used by --no-open and unit tests).
func (c *Controller) SetNoOpen(v bool) {
	c.mu.Lock()
	c.noOpen = v
	c.mu.Unlock()
}

func (c *Controller) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subs, ch)
		c.mu.Unlock()
	}
}

func (c *Controller) notifyLocked() {
	for ch := range c.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (c *Controller) appendEventLocked(ev UIEvent) {
	c.nextEventID++
	ev.ID = c.nextEventID
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	c.events = append(c.events, ev)
	if len(c.events) > maxEventHistory {
		c.events = append([]UIEvent(nil), c.events[len(c.events)-maxEventHistory:]...)
	}
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	var connectedCopy *time.Time
	if c.connectedAt != nil {
		t := *c.connectedAt
		connectedCopy = &t
	}
	eventsCopy := append([]UIEvent(nil), c.events...)
	if eventsCopy == nil {
		eventsCopy = []UIEvent{}
	}
	hasEnvToken := false
	if envName, err := agent.TokenEnvName(c.profile.Name); err == nil && strings.TrimSpace(os.Getenv(envName)) != "" {
		hasEnvToken = true
	}
	var pairingCopy *PairingState
	if c.pairing != nil {
		p := *c.pairing
		pairingCopy = &p
	}
	pub := c.profile.Public((c.runtimeToken != "" && c.profile.tokenMatchesName()) || hasEnvToken)
	return Snapshot{
		State:        c.state,
		ConnectedAt:  connectedCopy,
		LastError:    c.lastError,
		RetryInSec:   c.retryInSec,
		Pairing:      pairingCopy,
		Version:      c.version,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Shell:        agent.DetectedShell(),
		ConfigPath:   c.configPath,
		TokenStorage: tokenStorageDescription(),
		Profile:      pub,
		CLIPreview:   buildCLIPreview(c.profile),
		Events:       eventsCopy,
	}
}

func (c *Controller) SaveConfig(req ConfigUpdate) error {
	hub := strings.TrimSpace(req.Hub)
	name := strings.TrimSpace(req.Name)
	if hub == "" {
		return errors.New("请填写 Hub 地址（例如 https://mcp.example.com）")
	}
	if err := agent.ValidateHubURL(hub); err != nil {
		return fmt.Errorf("Hub 地址无效: %w", err)
	}
	if err := agent.ValidateName(name); err != nil {
		return fmt.Errorf("设备名称无效（仅允许小写字母、数字和连字符，不能为 local）: %w", err)
	}
	dirs, err := normalizeAllowDirs(req.AllowDirs)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		return errors.New("请至少添加一个允许访问的工作目录")
	}

	c.mu.Lock()
	next := c.profile
	nameChanged := c.profile.Name != "" && c.profile.Name != name
	next.Hub = hub
	next.Name = name
	next.AllowDirs = dirs
	next.AllowExec = req.AllowExec
	next.AutoConnect = req.AutoConnect
	next.SaveToken = req.SaveToken

	tokenInput := strings.TrimSpace(req.Token)
	nextRuntimeToken := c.runtimeToken
	if tokenInput != "" {
		nextRuntimeToken = tokenInput
		next.TokenDeviceName = name
	} else if nameChanged {
		nextRuntimeToken = ""
		next.TokenSealed = ""
		next.TokenDeviceName = ""
	}
	if next.SaveToken {
		if nextRuntimeToken != "" {
			sealed, err := sealToken(nextRuntimeToken)
			if err != nil {
				c.mu.Unlock()
				return fmt.Errorf("加密保存令牌失败: %w", err)
			}
			next.TokenSealed = sealed
			if next.TokenDeviceName == "" {
				next.TokenDeviceName = name
			}
		}
	} else {
		next.TokenSealed = ""
	}

	if err := saveProfile(c.configPath, next); err != nil {
		c.mu.Unlock()
		return err
	}
	wasRunning := c.cancelRun != nil
	c.profile = next
	c.runtimeToken = nextRuntimeToken
	c.appendEventLocked(UIEvent{
		Kind:    "system",
		Summary: fmt.Sprintf("配置已保存（%s · %d 个目录 · 执行权限:%v）", next.Name, len(next.AllowDirs), next.AllowExec),
	})
	c.notifyLocked()
	c.mu.Unlock()

	if req.ConnectNow || wasRunning {
		return c.Connect()
	}
	return nil
}

func (c *Controller) ClearToken() error {
	c.mu.Lock()
	next := c.profile
	next.TokenSealed = ""
	next.TokenDeviceName = ""
	if err := saveProfile(c.configPath, next); err != nil {
		c.mu.Unlock()
		return err
	}
	c.profile = next
	c.runtimeToken = ""
	c.appendEventLocked(UIEvent{Kind: "system", Summary: "已清除本地保存的配对令牌"})
	c.notifyLocked()
	c.mu.Unlock()
	return nil
}

func (c *Controller) ClearEvents() {
	c.mu.Lock()
	c.events = c.events[:0]
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *Controller) AutoStartIfConfigured() {
	c.mu.Lock()
	should := c.profile.AutoConnect && c.profile.Hub != "" && c.profile.Name != "" && len(c.profile.AllowDirs) > 0
	c.mu.Unlock()
	if !should {
		return
	}
	if err := c.Connect(); err != nil {
		c.log.Warn("自动连接失败", "err", err)
	}
}

func (c *Controller) Connect() error {
	c.stopActive(false)

	c.mu.Lock()
	prof := c.profile
	tok := ""
	if prof.tokenMatchesName() {
		tok = c.runtimeToken
	}
	if tok == "" && prof.TokenSealed != "" && prof.tokenMatchesName() {
		if plain, err := unsealToken(prof.TokenSealed); err == nil {
			tok = plain
			c.runtimeToken = plain
		}
	}
	c.mu.Unlock()

	if prof.Hub == "" || prof.Name == "" || len(prof.AllowDirs) == 0 {
		return errors.New("请先填写 Hub 地址、设备名称并至少添加一个工作目录")
	}
	if tok == "" {
		if envName, err := agent.TokenEnvName(prof.Name); err == nil {
			tok = strings.TrimSpace(os.Getenv(envName))
		}
	}
	if tok == "" {
		return c.StartPairing()
	}

	c.mu.Lock()
	c.gen++
	gen := c.gen
	c.mu.Unlock()

	client, err := agent.NewClient(agent.ClientOptions{
		Hub:       prof.Hub,
		Token:     tok,
		Name:      prof.Name,
		Version:   c.version,
		AllowDirs: prof.AllowDirs,
		AllowExec: prof.AllowExec,
		NoPrompt:  true,
		Log:       c.log,
		OnEvent: func(ev agent.Event) {
			c.handleAgentEvent(gen, ev)
		},
	})
	if err != nil {
		c.mu.Lock()
		c.lastError = err.Error()
		c.appendEventLocked(UIEvent{Kind: "disconnected", Error: err.Error()})
		c.notifyLocked()
		c.mu.Unlock()
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	c.mu.Lock()
	c.cancelRun = cancel
	c.client = client
	c.doneCh = done
	c.state = "connecting"
	c.lastError = ""
	c.retryInSec = 0
	c.connectedAt = nil
	c.appendEventLocked(UIEvent{Kind: "connecting", Summary: prof.Hub})
	c.notifyLocked()
	c.mu.Unlock()

	go func() {
		defer close(done)
		defer client.Close()
		_ = client.Run(runCtx)
		c.mu.Lock()
		if c.gen == gen {
			c.cancelRun = nil
			c.client = nil
			c.doneCh = nil
			c.state = "disconnected"
			c.connectedAt = nil
			c.retryInSec = 0
			c.notifyLocked()
		}
		c.mu.Unlock()
	}()

	return nil
}

// StartPairing initiates one-click Hub browser-approval pairing for the configured device name.
func (c *Controller) StartPairing() error {
	c.stopActive(false)

	c.mu.Lock()
	prof := c.profile
	noOpen := c.noOpen
	c.mu.Unlock()

	if prof.Hub == "" || prof.Name == "" || len(prof.AllowDirs) == 0 {
		return errors.New("请先填写 Hub 地址、设备名称并至少添加一个工作目录")
	}
	if err := agent.ValidateHubURL(prof.Hub); err != nil {
		return fmt.Errorf("Hub 地址无效: %w", err)
	}
	if err := agent.ValidateName(prof.Name); err != nil {
		return fmt.Errorf("设备名称无效: %w", err)
	}

	hubBase := strings.TrimRight(strings.TrimSpace(prof.Hub), "/")
	body, _ := json.Marshal(map[string]string{
		"name": prof.Name,
		"os":   runtime.GOOS,
	})
	req, err := http.NewRequest(http.MethodPost, hubBase+"/agent/pair/start", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造配对请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接 Hub 发起配对: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return errors.New("当前 Hub 版本尚未支持一键配对，请先升级 Hub 至 v0.2.0 或在高级选项中手动填入令牌")
	}
	if resp.StatusCode != http.StatusOK {
		var errPayload struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errPayload) == nil && errPayload.Error != "" {
			return errors.New(errPayload.Error)
		}
		return fmt.Errorf("Hub 拒绝配对请求 (HTTP %d)", resp.StatusCode)
	}
	var startRes struct {
		Code       string `json:"code"`
		PollToken  string `json:"poll_token"`
		ApproveURL string `json:"approve_url"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &startRes); err != nil || startRes.Code == "" || startRes.PollToken == "" {
		return errors.New("Hub 返回的配对数据无效")
	}
	approveURL := strings.TrimSpace(startRes.ApproveURL)
	if approveURL == "" {
		approveURL = hubBase + "/agents?code=" + startRes.Code
	}
	ttl := time.Duration(startRes.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	expiresAt := time.Now().Add(ttl)
	pairCtx, pairCancel := context.WithCancel(context.Background())

	c.mu.Lock()
	c.pairGen++
	pGen := c.pairGen
	c.pairCancel = pairCancel
	c.pairing = &PairingState{
		Code:       startRes.Code,
		ApproveURL: approveURL,
		DeviceName: prof.Name,
		ExpiresAt:  expiresAt,
	}
	c.state = "pairing"
	c.lastError = ""
	c.retryInSec = 0
	c.appendEventLocked(UIEvent{
		Kind:    "system",
		Summary: fmt.Sprintf("已向 Hub 发起配对（设备 %s · 验证码 %s），请在浏览器批准接入", prof.Name, startRes.Code),
	})
	c.notifyLocked()
	c.mu.Unlock()

	if !noOpen && approveURL != "" {
		go func(u string) {
			_ = openBrowser(u)
		}(approveURL)
	}

	go c.pollPairingLoop(pairCtx, pGen, hubBase, prof.Name, startRes.Code, startRes.PollToken, expiresAt)
	return nil
}

func (c *Controller) pollPairingLoop(ctx context.Context, pGen uint64, hubBase, deviceName, code, pollToken string, expiresAt time.Time) {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(expiresAt) {
				c.finishPairingWithError(pGen, "配对请求已超时，请重新点击连接发起配对")
				return
			}
			body, _ := json.Marshal(map[string]string{
				"code":       code,
				"poll_token": pollToken,
			})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, hubBase+"/agent/pair/poll", bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.httpClient.Do(req)
			if err != nil {
				continue
			}
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				continue
			}
			var pollRes struct {
				Status string `json:"status"`
				Name   string `json:"name"`
				Token  string `json:"token"`
			}
			if err := json.Unmarshal(raw, &pollRes); err != nil {
				continue
			}
			switch pollRes.Status {
			case "approved":
				if pollRes.Token == "" {
					c.finishPairingWithError(pGen, "Hub 批准了配对但未返回令牌，请重试")
					return
				}
				c.applyPairedTokenAndConnect(pGen, deviceName, pollRes.Token)
				return
			case "rejected":
				c.finishPairingWithError(pGen, "Hub 管理员拒绝了本次配对请求")
				return
			case "expired":
				c.finishPairingWithError(pGen, "配对请求已过期，请重新发起配对")
				return
			}
		}
	}
}

func (c *Controller) applyPairedTokenAndConnect(pGen uint64, deviceName, token string) {
	c.mu.Lock()
	if c.pairGen != pGen {
		c.mu.Unlock()
		return
	}
	c.pairCancel = nil
	c.pairing = nil
	c.runtimeToken = token
	next := c.profile
	next.TokenDeviceName = deviceName
	if next.SaveToken {
		if sealed, err := sealToken(token); err == nil {
			next.TokenSealed = sealed
		}
	} else {
		next.TokenSealed = ""
	}
	if err := saveProfile(c.configPath, next); err == nil {
		c.profile = next
	}
	c.appendEventLocked(UIEvent{
		Kind:    "system",
		Summary: fmt.Sprintf("Hub 已批准设备 %s 配对，正在自动建立加密连接…", deviceName),
	})
	c.notifyLocked()
	c.mu.Unlock()

	if err := c.Connect(); err != nil {
		c.mu.Lock()
		c.lastError = err.Error()
		c.notifyLocked()
		c.mu.Unlock()
	}
}

func (c *Controller) finishPairingWithError(pGen uint64, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pairGen != pGen {
		return
	}
	c.pairCancel = nil
	c.pairing = nil
	c.state = "disconnected"
	c.lastError = msg
	c.appendEventLocked(UIEvent{Kind: "disconnected", Error: msg})
	c.notifyLocked()
}

// CancelPairing aborts any active Hub pairing flow.
func (c *Controller) CancelPairing() {
	c.mu.Lock()
	pCancel := c.pairCancel
	wasPairing := c.pairing != nil
	c.pairGen++
	c.pairCancel = nil
	c.pairing = nil
	if wasPairing {
		c.state = "disconnected"
		c.appendEventLocked(UIEvent{Kind: "system", Summary: "已取消设备配对请求"})
		c.notifyLocked()
	}
	c.mu.Unlock()
	if pCancel != nil {
		pCancel()
	}
}

// OpenApproveURL opens the active pairing approval URL in the default browser.
func (c *Controller) OpenApproveURL() error {
	c.mu.Lock()
	u := ""
	if c.pairing != nil {
		u = c.pairing.ApproveURL
	}
	noOpen := c.noOpen
	c.mu.Unlock()
	if u == "" {
		return errors.New("当前没有进行中的配对请求")
	}
	if noOpen {
		return nil
	}
	return openBrowser(u)
}

func (c *Controller) Disconnect() {
	c.stopActive(true)
}

func (c *Controller) Close() {
	c.stopActive(false)
}

func (c *Controller) stopActive(logUserAction bool) {
	c.mu.Lock()
	cancel := c.cancelRun
	done := c.doneCh
	pCancel := c.pairCancel
	wasPairing := c.pairing != nil
	wasRunning := cancel != nil
	c.pairGen++
	c.pairCancel = nil
	c.pairing = nil
	c.gen++
	c.cancelRun = nil
	c.client = nil
	c.doneCh = nil
	c.state = "disconnected"
	c.connectedAt = nil
	c.retryInSec = 0
	if logUserAction && wasRunning {
		c.appendEventLocked(UIEvent{Kind: "system", Summary: "已手动断开与 Hub 的连接"})
	} else if logUserAction && wasPairing {
		c.appendEventLocked(UIEvent{Kind: "system", Summary: "已取消设备配对请求"})
	}
	c.notifyLocked()
	c.mu.Unlock()

	if pCancel != nil {
		pCancel()
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Controller) handleAgentEvent(gen uint64, ev agent.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != gen {
		return
	}
	switch ev.Kind {
	case "connecting":
		if c.state != "reconnecting" {
			c.state = "connecting"
		}
	case "connected":
		now := ev.Time
		if now.IsZero() {
			now = time.Now()
		}
		c.state = "connected"
		c.connectedAt = &now
		c.lastError = ""
		c.everConnGen = gen
		c.retryInSec = 0
		c.appendEventLocked(UIEvent{
			Time:    now,
			Kind:    "connected",
			Summary: "已连接 Hub · 工作区 " + ev.Summary,
		})
	case "disconnected":
		c.connectedAt = nil
		if ev.Error != "" {
			c.state = "reconnecting"
			if c.everConnGen != gen && (strings.Contains(ev.Error, "status code 401") || strings.Contains(ev.Error, "status code 503")) {
				cancel := c.cancelRun
				c.gen++
				c.cancelRun = nil
				c.client = nil
				c.doneCh = nil
				c.state = "disconnected"
				c.retryInSec = 0
				c.runtimeToken = ""
				next := c.profile
				next.TokenSealed = ""
				next.TokenDeviceName = ""
				_ = saveProfile(c.configPath, next)
				c.profile = next
				c.lastError = "当前设备的配对凭证已失效或尚未在 Hub 批准，请点击「连接 Hub」重新一键配对。"
				c.appendEventLocked(UIEvent{Time: ev.Time, Kind: "disconnected", Error: c.lastError})
				c.notifyLocked()
				if cancel != nil {
					go cancel()
				}
				return
			}
			c.lastError = ev.Error
			c.retryInSec = ev.RetryInSec
			c.appendEventLocked(UIEvent{
				Time:       ev.Time,
				Kind:       "disconnected",
				Error:      ev.Error,
				RetryInSec: ev.RetryInSec,
			})
		} else {
			c.state = "disconnected"
			c.retryInSec = 0
		}
	case "operation":
		c.appendEventLocked(UIEvent{
			Time:       ev.Time,
			Kind:       "operation",
			Method:     ev.Method,
			Summary:    ev.Summary,
			DurationMs: ev.DurationMs,
			Error:      ev.Error,
		})
	}
	c.notifyLocked()
}

func normalizeAllowDirs(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("目录路径无效 %q: %w", trimmed, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("目录不存在或无法访问 %q: %w", abs, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("路径不是文件夹: %q", abs)
		}
		key := abs
		if runtime.GOOS == "windows" {
			key = strings.ToLower(abs)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, abs)
	}
	return out, nil
}

func buildCLIPreview(p Profile) string {
	bin := "./omni-mcp"
	if runtime.GOOS == "windows" {
		bin = `.\\omni-mcp-windows-amd64.exe`
	}
	parts := []string{bin, "agent"}
	if p.Hub != "" {
		parts = append(parts, "--hub", p.Hub)
	} else {
		parts = append(parts, "--hub", "https://mcp.example.com")
	}
	if p.Name != "" {
		parts = append(parts, "--name", p.Name)
	} else {
		parts = append(parts, "--name", "my-workstation")
	}
	if len(p.AllowDirs) == 0 {
		parts = append(parts, "--allow-dir", "'D:\\Projects\\App'")
	} else {
		for _, d := range p.AllowDirs {
			parts = append(parts, "--allow-dir", "'"+d+"'")
		}
	}
	if p.AllowExec {
		parts = append(parts, "--allow-exec")
	}
	return strings.Join(parts, " ")
}
