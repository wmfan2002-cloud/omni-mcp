// Package app 负责把各层装配成一个可运行的 HTTP 服务。
package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"omni-mcp/internal/agent"
	"omni-mcp/internal/audit"
	"omni-mcp/internal/auth"
	"omni-mcp/internal/config"
	"omni-mcp/internal/fleet"
	"omni-mcp/internal/mcp"
	"omni-mcp/internal/media"
	"omni-mcp/internal/policy"
	"omni-mcp/internal/process"
	"omni-mcp/internal/tools"
	"omni-mcp/internal/upstream"
)

// Version 由构建时通过 -ldflags 注入。
var Version = "dev"

const fleetInstructions = `你连着用户的一组服务器，机器列表见各工具的 target 参数，实时状态用 servers 查。

用户问到任何机器的状态、服务、日志、配置、网络、磁盘时：先调工具拿到真实数据再回答，
不要只给命令让用户自己去跑。不知道在哪台机器上：先调 servers。

标了【需审批】的机器，第一次连接会给用户手机发授权申请并等待；超时就告诉用户去批准，批准后原样重试。
用户拒绝了就停下来问用户，不要换办法绕过。

危险命令会被拦下并返回 confirm_token：先向用户说明后果，用户同意后再带上 token 重发。

改远端文件优先用 apply_patch，它按上下文定位，不会覆盖没读过的内容。`

const agentInstructions = `Windows、Linux 与 macOS Agent 会在运行 omni-mcp agent 后通过 HTTPS 主动接入，不需要开放设备入站端口或配置公网 IP。设备出现在 target 和 servers 列表里。

先用 read、glob 和 search 检查 Agent 通过 --allow-dir 注册的工作区。shell 命令执行需要 Agent 使用 --allow-exec 启动；执行权限等同于启动该 Agent 的本地用户。`

const singleInstructions = `这是 Hub 所在机器上的基础工具集，你可以用它直接操作这台机器。

工具分工：
- shell：按目标操作系统的 shell 语法执行命令。长任务用 run_in_background=true，再配合 shell_output / shell_kill。
- read / apply_patch：读文件和改文件。read 默认带行号，apply_patch 靠上下文定位。
- search：按内容搜正则。glob：按文件名模式找文件。

建议的工作方式：先用 glob / search 摸清情况，用 read 确认要改的内容，
再用 apply_patch 落改动，最后用 shell 跑测试或重启服务验证。

关于安全拦截：绝大多数命令直接执行。少数破坏性操作会被拦下并返回 confirm_token，
这时应当先向用户说明这条命令会做什么、有什么后果，得到同意后再带 token 重发。
不要在用户没表态的情况下自己确认。`

func serverInstructions(f *fleet.Fleet) string {
	if !f.Enabled() {
		return singleInstructions + "\n\n" + agentInstructions
	}
	switch def := f.DefaultTarget(); def {
	case fleet.LocalTarget:
		return fleetInstructions + "\n\n" + agentInstructions + "\n\n文件工具也有 target，默认 local（Hub 所在机器）。"
	case "":
		return fleetInstructions + "\n\n" + agentInstructions + "\n\n所有命令与文件工具都必须显式指定 target。"
	default:
		return fmt.Sprintf("%s\n\n%s\n\n命令与文件工具的默认 target 为 %s，操作其它机器时显式指定 target。", fleetInstructions, agentInstructions, def)
	}
}

// Built 是装配好的服务：一个 http.Handler 加上关停时要做的清理。
type Built struct {
	Handler http.Handler
	Tools   []string
	Close   func()
}

// BuildOptions 提供嵌入运行（如桌面端本机网关模式）时的可选装配参数。
type BuildOptions struct {
	// ExtraAgentTokens 为内置回环 Agent 预置内存配对令牌，不写入环境变量或磁盘。
	ExtraAgentTokens map[string]string
}

// Build 按配置装配整个服务，但不监听端口。抽出来是为了让集成测试能用
// httptest 跑完整的 OAuth + MCP 链路，而不必去猜端口。
func Build(cfg *config.Config, log *slog.Logger) (*Built, error) {
	return BuildWithOptions(cfg, log, BuildOptions{})
}

// BuildWithOptions 按配置与附加运行选项装配整个服务。
func BuildWithOptions(cfg *config.Config, log *slog.Logger, opts BuildOptions) (*Built, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	aud, err := audit.New(cfg.Audit, log)
	if err != nil {
		return nil, fmt.Errorf("初始化审计日志失败: %w", err)
	}

	store, err := auth.OpenStore(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}
	authSrv, err := auth.New(cfg, store, aud, log)
	if err != nil {
		return nil, err
	}

	shellGuard, err := policy.NewShellGuard(cfg.Policy, selfNames())
	if err != nil {
		return nil, fmt.Errorf("加载 shell 策略失败: %w", err)
	}
	upMgr := upstream.New(cfg.Upstreams, log)
	deviceFleet := fleet.New(cfg.Fleet, cfg.Tools.Shell.Path, cfg.Tools.Shell.Env)
	agentTokens := agent.TokensFromEnvironment(os.Environ())
	for k, v := range opts.ExtraAgentTokens {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			agentTokens[k] = strings.TrimSpace(v)
		}
	}
	agents := agent.NewManager(agentTokens, log, func(devices []agent.Info) {
		hosts := make([]fleet.Host, 0, len(devices))
		for _, device := range devices {
			hosts = append(hosts, fleet.Host{
				Name: device.Name, Desc: device.OS + " Agent · " + device.Workdir, Workdir: device.Workdir,
				Agent: true, Windows: device.OS == "windows", OS: device.OS, AllowExec: device.AllowExec,
			})
		}
		deviceFleet.SetAgentHosts(hosts)
	})
	if err := agents.AttachStore(cfg.Server.DataDir); err != nil {
		return nil, fmt.Errorf("加载已配对设备列表失败: %w", err)
	}
	for _, host := range deviceFleet.Hosts() {
		agents.ReserveNames([]string{host.Name})
	}
	deviceFleet.SetAgentControl(agents)
	deviceFleet.SetAgentEnabled(agents.Enabled())
	deps := &tools.Deps{
		Cfg:       cfg,
		Paths:     policy.NewPathGuard(cfg.Policy, cfg.Tools.Shell.DefaultWorkdir),
		Shell:     shellGuard,
		Confirm:   policy.NewConfirmStore(cfg.Policy.ConfirmTTL),
		Audit:     aud,
		Procs:     tools.NewProcManager(cfg.Tools.Shell.MaxBackground),
		Fleet:     deviceFleet,
		Upstreams: upMgr,
		Log:       log,
	}
	if iu := cfg.Tools.FS.ImageURL; iu.Enabled {
		deps.Media = media.New(media.Options{
			TTL: iu.TTL, OneTime: iu.OneTime, MaxItems: iu.MaxItems, MaxTotalBytes: iu.MaxTotalBytes,
		})
		log.Info("临时图片链接已启用", "ttl", iu.TTL, "one_time", iu.OneTime)
	}

	mcpSrv := mcp.NewServer(mcp.Implementation{
		Name: "omni-mcp", Title: "统一多机基础工具集", Version: Version,
	}, func() string {
		// 每次 initialize 现算：上游可能刚连上，说明要跟着变。
		return serverInstructions(deps.Fleet) + upMgr.Instructions()
	}, log)
	mcpSrv.SetAuditor(aud)
	mcpSrv.Register(tools.All(deps)...)
	// 工具集合已注册；后台立即拉取上游定义并按周期刷新。
	upMgr.Start()
	if len(cfg.Upstreams) > 0 {
		log.Info("上游 MCP 透传已启用", "upstreams", len(cfg.Upstreams))
	}

	transport := mcp.NewHTTPTransport(mcpSrv, mcp.HTTPOptions{
		BaseURL:        cfg.Server.BaseURL,
		AllowedOrigins: cfg.Server.AllowedOrigins,
		SessionTTL:     cfg.Server.SessionTTL,
		StrictSessions: cfg.Server.StrictSessions,
		Log:            log,
	})

	mux := http.NewServeMux()
	authSrv.Routes(mux)
	mux.Handle("/mcp", authSrv.Protect(transport))
	// 有些客户端习惯在末尾带斜杠。
	mux.Handle("/mcp/", authSrv.Protect(transport))
	mux.Handle("/agent/ws", agents)
	mux.HandleFunc("/agent/pair/start", agents.HandlePairStart(cfg.Server.BaseURL))
	mux.HandleFunc("/agent/pair/poll", agents.HandlePairPoll())
	agentsPortal := newAgentsPortal(cfg, authSrv, agents, log)
	mux.Handle("/agents", agentsPortal)
	mux.Handle("/agents/", agentsPortal)
	if deps.Media != nil {
		// 不挂 OAuth：拿到链接去下载的一方（客户端侧的下载工具）没有 Bearer。
		// 保护靠 256-bit 随机 token + 短 TTL，token 不进日志（见 logPath）。
		mux.Handle(media.PathPrefix, deps.Media.Handler())
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"status": "ok", "version": Version,
			"license": "AGPL-3.0-or-later", "source": cfg.Server.SourceURL,
			"tools": mcpSrv.ToolNames(), "auth": cfg.Auth.Enabled,
			"pending_confirms": deps.Confirm.Pending(),
			"oauth":            store.Stats(),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderLanding(w, cfg, mcpSrv.ToolNames())
	})

	return &Built{
		Handler: withRecover(withRequestLog(log, mux)),
		Tools:   mcpSrv.ToolNames(),
		Close: func() {
			agents.Close()
			upMgr.Close()
			if deps.Media != nil {
				deps.Media.Close()
			}
			deps.Procs.KillAll()
			_ = aud.Close()
		},
	}, nil
}

// Run 启动服务并阻塞直到收到退出信号。
func Run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	log := newLogger()

	built, err := Build(cfg, log)
	if err != nil {
		return err
	}
	defer built.Close()

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           built.Handler,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		// 后台任务与 SSE 需要长连接，空闲超时不能太短。
		IdleTimeout: 10 * time.Minute,
	}

	log.Info("omni-mcp 启动",
		"version", Version, "listen", cfg.Server.Listen, "base_url", cfg.Server.BaseURL,
		"auth", cfg.Auth.Enabled, "tools", len(built.Tools))
	if !cfg.Auth.Enabled {
		log.Warn("鉴权已关闭：任何能访问该端口的人都可以在这台机器上执行命令，仅限本机调试使用")
	}
	if strings.HasPrefix(cfg.Server.BaseURL, "http://") &&
		!strings.Contains(cfg.Server.BaseURL, "127.0.0.1") && !strings.Contains(cfg.Server.BaseURL, "localhost") {
		log.Warn("base_url 不是 https，ChatGPT 无法连接；请在反代层配置 TLS", "base_url", cfg.Server.BaseURL)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), process.Interrupts()...)
	defer stop()
	select {
	case err := <-errCh:
		return fmt.Errorf("监听 %s 失败: %w", cfg.Server.Listen, err)
	case <-ctx.Done():
		log.Info("收到退出信号，正在关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("OMNI_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if strings.EqualFold(os.Getenv("OMNI_LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// selfNames 给 shell 策略提供"本服务自身"的名字，用于识别自杀式命令。
func selfNames() []string {
	names := []string{"omni-mcp"}
	if exe, err := os.Executable(); err == nil {
		if base := baseName(exe); base != "" && base != "omni-mcp" {
			names = append(names, base)
		}
	}
	return names
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// withRecover 兜住 handler 里的 panic，避免单个请求打崩进程。
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("HTTP handler panic", "panic", rec, "path", logPath(r.URL.Path))
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(s.ResponseWriter).Hijack()
	if err == nil {
		s.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush 透传给底层，SSE 依赖它。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func withRequestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" {
			return
		}
		log.Debug("http", "method", r.Method, "path", logPath(r.URL.Path),
			"status", rec.status, "dur_ms", time.Since(start).Milliseconds())
	})
}

// logPath 抹掉路径里的凭据：/media/<token> 的 token 本身就是下载凭据，不能落日志。
// 先 Clean 再判断，"//media/<token>" 这类会被 mux 重定向的写法也一并抹掉。
func logPath(p string) string {
	if strings.HasPrefix(path.Clean(p)+"/", media.PathPrefix) {
		return media.PathPrefix + "[redacted]"
	}
	return p
}
