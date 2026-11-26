package agentui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"omni-mcp/internal/agent"
	"omni-mcp/internal/app"
	"omni-mcp/internal/config"
	"omni-mcp/internal/idgen"
)

const (
	ModeRemote         = "remote"
	ModeGateway        = "gateway"
	defaultGatewayPort = 8485
	gatewayUsername    = "admin"
)

// GatewayStatus is the sanitized local gateway status sent to the Desktop UI.
// Secrets (passwords, bearer tokens, tunnel tokens) are NEVER included here.
type GatewayStatus struct {
	State            string     `json:"state"` // stopped | downloading | starting | running | error
	Tunnel           string     `json:"tunnel"`
	LocalURL         string     `json:"local_url,omitempty"`
	PublicURL        string     `json:"public_url,omitempty"`
	MCPEndpoint      string     `json:"mcp_endpoint,omitempty"`
	Username         string     `json:"username"`
	CloudflaredReady bool       `json:"cloudflared_ready"`
	StatusNote       string     `json:"status_note,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
}

// GatewayCredentials is returned only on explicit CSRF-protected reveal/rotate requests.
type GatewayCredentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	BearerToken string `json:"bearer_token"`
}

type swappableHandler struct {
	current atomic.Pointer[http.Handler]
}

func (s *swappableHandler) Set(h http.Handler) {
	s.current.Store(&h)
}

func (s *swappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if hp := s.current.Load(); hp != nil && *hp != nil {
		(*hp).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"status":"starting","message":"omni-mcp local gateway is initializing"}`))
}

type gatewayRuntime struct {
	cancel      context.CancelFunc
	localLn     net.Listener
	localSrv    *http.Server
	publicSrv   *http.Server
	tunnel      activeTunnel
	built       *app.Built
	agentClient *agent.Client
	agentDone   chan struct{}

	mu        sync.Mutex
	closeOnce sync.Once
	closed    bool
}

func (g *gatewayRuntime) setBuilt(b *app.Built) bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		if b != nil && b.Close != nil {
			b.Close()
		}
		return false
	}
	g.built = b
	g.mu.Unlock()
	return true
}

func (g *gatewayRuntime) setTunnel(t activeTunnel) bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		if t != nil {
			_ = t.Close()
		}
		return false
	}
	g.tunnel = t
	g.mu.Unlock()
	return true
}

func (g *gatewayRuntime) setPublicSrv(s *http.Server) bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		if s != nil {
			_ = s.Close()
		}
		return false
	}
	g.publicSrv = s
	g.mu.Unlock()
	return true
}

func (g *gatewayRuntime) setAgent(c *agent.Client, done chan struct{}) bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		if c != nil {
			c.Close()
		}
		return false
	}
	g.agentClient = c
	g.agentDone = done
	g.mu.Unlock()
	return true
}

func (g *gatewayRuntime) Close() {
	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.closed = true
		cancel := g.cancel
		pubSrv := g.publicSrv
		locSrv := g.localSrv
		tun := g.tunnel
		ln := g.localLn
		agDone := g.agentDone
		agClient := g.agentClient
		built := g.built
		g.mu.Unlock()
		if cancel != nil {
			cancel()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if pubSrv != nil {
			_ = pubSrv.Shutdown(ctx)
		}
		if locSrv != nil {
			_ = locSrv.Shutdown(ctx)
		}
		if tun != nil {
			_ = tun.Close()
		}
		if ln != nil {
			_ = ln.Close()
		}
		if agDone != nil {
			select {
			case <-agDone:
			case <-time.After(2 * time.Second):
			}
		} else if agClient != nil {
			agClient.Close()
		}
		if built != nil {
			built.Close()
		}
	})
}

func normalizeMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), ModeGateway) {
		return ModeGateway
	}
	return ModeRemote
}

func defaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil {
		return "my-pc"
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		host = host[:idx]
	}
	var sb strings.Builder
	lastDash := false
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			lastDash = false
		} else if sb.Len() > 0 && !lastDash {
			sb.WriteByte('-')
			lastDash = true
		}
		if sb.Len() >= 28 {
			break
		}
	}
	clean := strings.Trim(sb.String(), "-")
	if clean == "" || clean == "local" || clean[0] < 'a' || clean[0] > 'z' {
		if clean != "" && clean != "local" {
			clean = "pc-" + clean
		} else {
			clean = "my-pc"
		}
	}
	if len(clean) > 40 {
		clean = clean[:40]
	}
	if err := agent.ValidateName(clean); err != nil {
		return "my-pc"
	}
	return clean
}

func generateGatewayPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	chars := make([]byte, 12)
	for i, b := range raw {
		chars[i] = alphabet[int(b)%len(alphabet)]
	}
	return fmt.Sprintf("omni-%s-%s-%s", string(chars[0:4]), string(chars[4:8]), string(chars[8:12]))
}

func generateGatewayBearer() string {
	return "omni_gw_" + idgen.Secret(24)
}

func (c *Controller) ensureGatewayCredentialsLocked() (password, bearer string, err error) {
	pw := c.runtimeGatewayPassword
	if pw == "" && strings.TrimSpace(c.profile.GatewayPasswordSealed) != "" {
		if plain, uerr := unsealToken(c.profile.GatewayPasswordSealed); uerr == nil && plain != "" {
			pw = plain
		}
	}
	bt := c.runtimeGatewayBearer
	if bt == "" && strings.TrimSpace(c.profile.GatewayBearerSealed) != "" {
		if plain, uerr := unsealToken(c.profile.GatewayBearerSealed); uerr == nil && plain != "" {
			bt = plain
		}
	}

	changed := false
	next := c.profile
	if pw == "" {
		pw = generateGatewayPassword()
		sealed, serr := sealToken(pw)
		if serr != nil {
			return "", "", fmt.Errorf("加密保存网关密码失败: %w", serr)
		}
		next.GatewayPasswordSealed = sealed
		changed = true
	}
	if bt == "" {
		bt = generateGatewayBearer()
		sealed, serr := sealToken(bt)
		if serr != nil {
			return "", "", fmt.Errorf("加密保存网关令牌失败: %w", serr)
		}
		next.GatewayBearerSealed = sealed
		changed = true
	}
	c.runtimeGatewayPassword = pw
	c.runtimeGatewayBearer = bt
	if changed {
		if serr := saveProfile(c.configPath, next); serr != nil {
			return "", "", serr
		}
		c.profile = next
	}
	return pw, bt, nil
}

// RevealGatewayCredentials returns the local gateway OAuth password and Bearer token.
func (c *Controller) RevealGatewayCredentials() (GatewayCredentials, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pw, bt, err := c.ensureGatewayCredentialsLocked()
	if err != nil {
		return GatewayCredentials{}, err
	}
	return GatewayCredentials{
		Username:    gatewayUsername,
		Password:    pw,
		BearerToken: bt,
	}, nil
}

// RotateGatewayCredentials generates fresh OAuth password & static Bearer token and restarts gateway if running.
func (c *Controller) RotateGatewayCredentials() (GatewayCredentials, error) {
	c.mu.Lock()
	oldProfile := c.profile
	pw := generateGatewayPassword()
	bt := generateGatewayBearer()
	sealedPW, err := sealToken(pw)
	if err != nil {
		c.mu.Unlock()
		return GatewayCredentials{}, fmt.Errorf("加密保存新密码失败: %w", err)
	}
	sealedBT, err := sealToken(bt)
	if err != nil {
		c.mu.Unlock()
		return GatewayCredentials{}, fmt.Errorf("加密保存新令牌失败: %w", err)
	}
	next := oldProfile
	next.GatewayPasswordSealed = sealedPW
	next.GatewayBearerSealed = sealedBT
	if err := saveProfile(c.configPath, next); err != nil {
		c.mu.Unlock()
		return GatewayCredentials{}, err
	}
	c.profile = next
	c.runtimeGatewayPassword = pw
	c.runtimeGatewayBearer = bt
	wasRunning := c.gwRuntime != nil
	c.appendEventLocked(UIEvent{Kind: "system", Summary: "已重置本机网关 OAuth 密码与 Bearer 令牌"})
	c.notifyLocked()
	c.mu.Unlock()
	if err := c.cleanupSealedTokens(removedSealedTokens(oldProfile, next)); err != nil {
		c.log.Warn("已轮换网关凭据，但旧凭据清理未完成", "err", err)
	}

	if wasRunning {
		if err := c.StartGateway(); err != nil {
			return GatewayCredentials{}, err
		}
	}
	return GatewayCredentials{
		Username:    gatewayUsername,
		Password:    pw,
		BearerToken: bt,
	}, nil
}

// ClearTunnelToken removes a saved ngrok or Cloudflare Named Tunnel token.
func (c *Controller) ClearTunnelToken(provider string) error {
	c.mu.Lock()
	oldProfile := c.profile
	next := oldProfile
	label := ""
	switch normalizeTunnelProvider(provider) {
	case TunnelNgrok:
		next.GatewayNgrokTokenSealed = ""
		c.runtimeNgrokToken = ""
		label = "ngrok Authtoken"
	case TunnelCloudflareNamed:
		next.GatewayCFTokenSealed = ""
		c.runtimeCFToken = ""
		label = "Cloudflare Tunnel Token"
	default:
		c.mu.Unlock()
		return errors.New("不支持清除该隧道类型的令牌")
	}
	if err := saveProfile(c.configPath, next); err != nil {
		c.mu.Unlock()
		return err
	}
	c.profile = next
	c.appendEventLocked(UIEvent{Kind: "system", Summary: "已清除本地保存的 " + label})
	c.notifyLocked()
	c.mu.Unlock()
	if err := c.cleanupSealedTokens(removedSealedTokens(oldProfile, next)); err != nil {
		return fmt.Errorf("令牌已从配置中移除，但系统密钥清理失败: %w", err)
	}
	return nil
}

// StartGateway starts the embedded local MCP Hub + loopback Agent + configured tunnel.
func (c *Controller) StartGateway() error {
	c.stopActive(false)
	c.stopGateway(false)

	c.mu.Lock()
	prof := c.profile
	if strings.TrimSpace(prof.Name) == "" {
		prof.Name = defaultDeviceName()
		c.profile.Name = prof.Name
		_ = saveProfile(c.configPath, c.profile)
	}
	tunnelKind := normalizeTunnelProvider(prof.GatewayTunnel)
	port := prof.GatewayPort
	if port < 0 || port > 65535 {
		port = defaultGatewayPort
	}
	if port == 0 && !prof.GatewayRandomPort {
		port = defaultGatewayPort
	}
	ngrokTok := c.runtimeNgrokToken
	if ngrokTok == "" && strings.TrimSpace(prof.GatewayNgrokTokenSealed) != "" {
		if plain, err := unsealToken(prof.GatewayNgrokTokenSealed); err == nil {
			ngrokTok = plain
			c.runtimeNgrokToken = plain
		}
	}
	cfTok := c.runtimeCFToken
	if cfTok == "" && strings.TrimSpace(prof.GatewayCFTokenSealed) != "" {
		if plain, err := unsealToken(prof.GatewayCFTokenSealed); err == nil {
			cfTok = plain
			c.runtimeCFToken = plain
		}
	}
	pw, bt, credErr := c.ensureGatewayCredentialsLocked()
	c.mu.Unlock()

	if credErr != nil {
		return credErr
	}
	if err := agent.ValidateName(prof.Name); err != nil {
		return fmt.Errorf("设备名称无效（仅允许小写字母、数字和连字符，不能为 local）: %w", err)
	}
	if len(prof.AllowDirs) == 0 {
		return errors.New("请先至少添加一个允许 AI 访问的本地文件夹")
	}
	switch tunnelKind {
	case TunnelNgrok:
		if strings.TrimSpace(ngrokTok) == "" {
			return errors.New("请先填写 ngrok Authtoken 并保存配置")
		}
		if _, err := normalizeNgrokDomain(prof.GatewayNgrokDomain); err != nil {
			return err
		}
	case TunnelCloudflareNamed:
		if _, err := validatePublicHTTPSURL(prof.GatewayCFPublicURL); err != nil {
			return err
		}
		if strings.TrimSpace(cfTok) == "" {
			return errors.New("请先填写 Cloudflare Named Tunnel Token 并保存配置")
		}
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)
	localLn, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("本机端口 %s 已被占用，请在设置中更换端口（或设为 0 自动分配）: %w", listenAddr, err)
	}
	actualLocalAddr := localLn.Addr().String()
	localURL := "http://" + actualLocalAddr

	swappable := &swappableHandler{}
	localSrv := &http.Server{
		Handler:           swappable,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       10 * time.Minute,
	}

	runCtx, cancel := context.WithCancel(context.Background())
	rt := &gatewayRuntime{
		cancel:   cancel,
		localLn:  localLn,
		localSrv: localSrv,
	}

	c.mu.Lock()
	c.gwGen++
	gen := c.gwGen
	c.gwRuntime = rt
	c.gwState = "starting"
	c.gwTunnel = tunnelKind
	c.gwLocalURL = localURL
	c.gwPublicURL = ""
	c.gwStatusNote = "正在初始化本机 MCP 网关…"
	c.gwLastError = ""
	c.gwStartedAt = nil
	c.appendEventLocked(UIEvent{Kind: "connecting", Summary: fmt.Sprintf("正在启动本机网关（%s · %s）", tunnelLabel(tunnelKind), actualLocalAddr)})
	c.notifyLocked()
	c.mu.Unlock()

	go func() {
		_ = localSrv.Serve(localLn)
	}()

	go c.bootGatewayAsync(runCtx, gen, rt, swappable, prof, tunnelKind, actualLocalAddr, localURL, pw, bt, ngrokTok, cfTok)
	return nil
}

func (c *Controller) bootGatewayAsync(
	ctx context.Context,
	gen uint64,
	rt *gatewayRuntime,
	swappable *swappableHandler,
	prof Profile,
	tunnelKind, actualLocalAddr, localURL, password, bearer, ngrokTok, cfTok string,
) {
	updateStatus := func(state, note string) {
		c.mu.Lock()
		if c.gwGen == gen {
			c.gwState = state
			c.gwStatusNote = note
			c.notifyLocked()
		}
		c.mu.Unlock()
	}

	fail := func(err error) {
		rt.Close()
		if errors.Is(err, context.Canceled) {
			return
		}
		c.mu.Lock()
		if c.gwGen == gen {
			c.gwRuntime = nil
			c.gwState = "error"
			c.gwPublicURL = ""
			c.gwStartedAt = nil
			c.gwStatusNote = ""
			c.gwLastError = err.Error()
			c.appendEventLocked(UIEvent{Kind: "disconnected", Error: err.Error()})
			c.notifyLocked()
		}
		c.mu.Unlock()
	}

	var (
		publicBaseURL = localURL
		tun           activeTunnel
		err           error
	)

	switch tunnelKind {
	case TunnelLocal:
		updateStatus("starting", "正在启动仅本机访问的 MCP 网关…")
	case TunnelCloudflareQuick:
		tun, err = startCloudflareQuickTunnel(ctx, localURL, updateStatus)
		if err != nil {
			fail(err)
			return
		}
		if !rt.setTunnel(tun) {
			return
		}
		publicBaseURL = tun.PublicURL()
	case TunnelCloudflareNamed:
		tun, err = startCloudflareNamedTunnel(ctx, prof.GatewayCFPublicURL, cfTok, updateStatus)
		if err != nil {
			fail(err)
			return
		}
		if !rt.setTunnel(tun) {
			return
		}
		publicBaseURL = tun.PublicURL()
	case TunnelNgrok:
		tun, err = startNgrokTunnel(ctx, ngrokTok, prof.GatewayNgrokDomain, c.log, updateStatus)
		if err != nil {
			fail(err)
			return
		}
		if !rt.setTunnel(tun) {
			return
		}
		publicBaseURL = tun.PublicURL()
	}

	if ctx.Err() != nil {
		rt.Close()
		return
	}

	updateStatus("starting", "正在装配内置 OAuth 2.1 + MCP 服务…")

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fail(fmt.Errorf("生成 OAuth 密码哈希失败: %w", err))
		return
	}

	internalAgentToken := idgen.Secret(32)
	exposeLocal := false
	cfg := config.Default()
	cfg.Server.Listen = actualLocalAddr
	cfg.Server.BaseURL = publicBaseURL
	cfg.Server.TrustProxy = true
	cfg.Server.DataDir = filepath.Join(filepath.Dir(c.configPath), "local-hub-data")
	cfg.Auth.Enabled = true
	cfg.Auth.AllowDynamicRegistration = true
	cfg.Auth.AllowUnauthenticatedInitialize = true
	cfg.Auth.Users = []config.User{{
		Username:     gatewayUsername,
		PasswordHash: string(hash),
	}}
	cfg.Auth.StaticTokens = []config.StaticToken{{
		Name:  "local-gateway",
		Token: bearer,
	}}
	cfg.Fleet.ExposeLocal = &exposeLocal
	cfg.Fleet.AgentOnly = true
	cfg.Fleet.DefaultTarget = prof.Name
	cfg.Audit.Enabled = true
	cfg.Audit.File = filepath.Join(cfg.Server.DataDir, "audit.jsonl")

	built, err := app.BuildWithOptions(cfg, c.log, app.BuildOptions{
		ExtraAgentTokens: map[string]string{
			prof.Name: internalAgentToken,
		},
	})
	if err != nil {
		fail(fmt.Errorf("初始化内置 MCP Hub 失败: %w", err))
		return
	}
	if !rt.setBuilt(built) {
		return
	}
	swappable.Set(built.Handler)

	if tun != nil && tun.Listener() != nil {
		pubSrv := &http.Server{
			Handler:           swappable,
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       10 * time.Minute,
		}
		if !rt.setPublicSrv(pubSrv) {
			return
		}
		go func() {
			_ = pubSrv.Serve(tun.Listener())
		}()
	}

	agentReadyCh := make(chan struct{}, 1)
	agentClient, err := agent.NewClient(agent.ClientOptions{
		Hub:       localURL,
		Token:     internalAgentToken,
		Name:      prof.Name,
		Version:   c.version,
		AllowDirs: prof.AllowDirs,
		AllowExec: prof.AllowExec,
		NoPrompt:  true,
		Log:       c.log,
		OnEvent: func(ev agent.Event) {
			if ev.Kind == "connected" {
				select {
				case agentReadyCh <- struct{}{}:
				default:
				}
			}
			c.handleGatewayAgentEvent(gen, ev)
		},
	})
	if err != nil {
		fail(fmt.Errorf("初始化本地工作区代理失败: %w", err))
		return
	}

	agentDone := make(chan struct{})
	if !rt.setAgent(agentClient, agentDone) {
		return
	}
	go func() {
		defer close(agentDone)
		defer agentClient.Close()
		_ = agentClient.Run(ctx)
	}()

	select {
	case <-agentReadyCh:
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		rt.Close()
		return
	}

	now := time.Now()
	c.mu.Lock()
	if c.gwGen != gen {
		c.mu.Unlock()
		rt.Close()
		return
	}
	c.gwState = "running"
	c.gwPublicURL = publicBaseURL
	c.gwStartedAt = &now
	c.gwStatusNote = "本机网关运行中"
	c.gwLastError = ""
	c.appendEventLocked(UIEvent{
		Time:    now,
		Kind:    "connected",
		Summary: fmt.Sprintf("本机网关已就绪 · %s/mcp", publicBaseURL),
	})
	c.notifyLocked()
	c.mu.Unlock()

	if tun != nil {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-tun.Done():
				if ctx.Err() != nil {
					return
				}
				tunErr := tun.Err()
				if tunErr == nil {
					tunErr = errors.New("公网隧道连接已断开，请重新启动本机网关")
				}
				fail(tunErr)
			}
		}()
	}
}

func (c *Controller) handleGatewayAgentEvent(gen uint64, ev agent.Event) {
	if ev.Kind != "operation" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gwGen != gen {
		return
	}
	c.appendEventLocked(UIEvent{
		Time:       ev.Time,
		Kind:       "operation",
		Method:     ev.Method,
		Summary:    ev.Summary,
		DurationMs: ev.DurationMs,
		Error:      ev.Error,
	})
	c.notifyLocked()
}

// StopGateway stops the embedded local MCP gateway and any active public tunnel.
func (c *Controller) StopGateway() {
	c.stopGateway(true)
}

func (c *Controller) stopGateway(logUserAction bool) {
	c.mu.Lock()
	rt := c.gwRuntime
	wasActive := rt != nil || c.gwState == "starting" || c.gwState == "downloading" || c.gwState == "running"
	c.gwGen++
	c.gwRuntime = nil
	c.gwState = "stopped"
	c.gwPublicURL = ""
	c.gwStartedAt = nil
	c.gwStatusNote = ""
	if logUserAction && wasActive {
		c.gwLastError = ""
		c.appendEventLocked(UIEvent{Kind: "system", Summary: "已停止本机独立网关与公网隧道"})
	}
	c.notifyLocked()
	c.mu.Unlock()

	if rt != nil {
		rt.Close()
	}
}

func tunnelLabel(kind string) string {
	switch normalizeTunnelProvider(kind) {
	case TunnelLocal:
		return "仅本机直连"
	case TunnelCloudflareNamed:
		return "Cloudflare Named Tunnel"
	case TunnelNgrok:
		return "ngrok 固定域名"
	default:
		return "Cloudflare 快速隧道"
	}
}
