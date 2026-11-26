package agentui

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"omni-mcp/internal/idgen"
)

//go:embed web/index.html
var indexHTML string

//go:embed web/logo.svg
var logoSVG []byte

//go:embed web/favicon.ico
var faviconICO []byte

const sessionCookieName = "omni_agent_ui_session"

// Options configures the local Agent UI HTTP server.
type Options struct {
	Listen     string
	ConfigPath string
	Version    string
	NoOpen     bool
	Log        *slog.Logger
}

// Server hosts the loopback-only Agent Web Console.
type Server struct {
	ctrl      *Controller
	token     string
	csrfToken string
	log       *slog.Logger
	onExit    func()
}

// NewServer creates an authenticated loopback handler for the Agent UI.
func NewServer(ctrl *Controller, log *slog.Logger, onExit func()) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		ctrl:      ctrl,
		token:     idgen.Secret(32),
		csrfToken: idgen.Secret(24),
		log:       log,
		onExit:    onExit,
	}
}

// BootstrapToken returns the one-time/session URL token required to open the console.
func (s *Server) BootstrapToken() string {
	return s.token
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/logo.svg", s.handleLogo)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/api/state", s.requireSession(s.handleState, false))
	mux.HandleFunc("/api/events", s.requireSession(s.handleEvents, false))
	mux.HandleFunc("/api/config", s.requireSession(s.handleConfig, true))
	mux.HandleFunc("/api/connect", s.requireSession(s.handleConnect, true))
	mux.HandleFunc("/api/disconnect", s.requireSession(s.handleDisconnect, true))
	mux.HandleFunc("/api/pair/start", s.requireSession(s.handlePairStart, true))
	mux.HandleFunc("/api/pair/cancel", s.requireSession(s.handlePairCancel, true))
	mux.HandleFunc("/api/pair/open", s.requireSession(s.handlePairOpen, true))
	mux.HandleFunc("/api/token/clear", s.requireSession(s.handleClearToken, true))
	mux.HandleFunc("/api/events/clear", s.requireSession(s.handleClearEvents, true))
	mux.HandleFunc("/api/pick-dir", s.requireSession(s.handlePickDir, true))
	mux.HandleFunc("/api/shutdown", s.requireSession(s.handleShutdown, true))
	return s.requireLoopbackHost(mux)
}

func (s *Server) requireLoopbackHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validToken(got string) bool {
	return len(got) == len(s.token) && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) validCSRF(got string) bool {
	return len(got) == len(s.csrfToken) && subtle.ConstantTimeCompare([]byte(got), []byte(s.csrfToken)) == 1
}

func (s *Server) hasSessionCookie(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	return err == nil && s.validToken(c.Value)
}

func (s *Server) requireSession(next http.HandlerFunc, requireCSRF bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hasSessionCookie(r) {
			writeJSONError(w, http.StatusForbidden, "本地会话未授权，请重新从终端链接打开控制台")
			return
		}
		if requireCSRF {
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if !s.validCSRF(r.Header.Get("X-Omni-UI-Token")) {
				writeJSONError(w, http.StatusForbidden, "CSRF 校验失败，请刷新页面后重试")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		if !s.validToken(tok) {
			http.Error(w, "无效的本地访问令牌", http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    s.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.hasSessionCookie(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Omni MCP Agent 本地控制台受会话令牌保护，请通过启动时打开的链接访问。\n"))
		return
	}
	page := strings.Replace(indexHTML, "__OMNI_CSRF_TOKEN__", s.csrfToken, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(logoSVG)
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(faviconICO)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func() bool {
		data, err := json.Marshal(s.ctrl.Snapshot())
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}
	updates, unsub := s.ctrl.Subscribe()
	defer unsub()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			if !send() {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	var req ConfigUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求参数解析失败: "+err.Error())
		return
	}
	if err := s.ctrl.SaveConfig(req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.Connect(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.ctrl.Disconnect()
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handlePairStart(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.StartPairing(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handlePairCancel(w http.ResponseWriter, r *http.Request) {
	s.ctrl.CancelPairing()
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handlePairOpen(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.OpenApproveURL(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClearToken(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.ClearToken(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handleClearEvents(w http.ResponseWriter, r *http.Request) {
	s.ctrl.ClearEvents()
	writeJSON(w, http.StatusOK, s.ctrl.Snapshot())
}

func (s *Server) handlePickDir(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	dir, err := pickDirectory(ctx)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": dir})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if s.onExit != nil {
		go func() {
			time.Sleep(150 * time.Millisecond)
			s.onExit()
		}()
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Run starts the local loopback UI server and blocks until ctx is canceled or UI shutdown is requested.
func Run(ctx context.Context, opts Options) error {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	listenAddr := strings.TrimSpace(opts.Listen)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:18485"
	}
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return fmt.Errorf("--listen 仅允许绑定本机回环地址（例如 127.0.0.1:18485）")
	}

	ctrl, err := NewController(opts.ConfigPath, opts.Version, opts.Log)
	if err != nil {
		return err
	}
	ctrl.SetNoOpen(opts.NoOpen)
	defer ctrl.Close()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	srv := NewServer(ctrl, opts.Log, cancel)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil && opts.Listen == "" {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	defer ln.Close()

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	uiURL := fmt.Sprintf("http://%s/?token=%s", ln.Addr().String(), srv.BootstrapToken())
	opts.Log.Info("Omni MCP Agent 可视化控制台已启动", "url", uiURL, "config", ctrl.configPath)

	ctrl.AutoStartIfConfigured()

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	if !opts.NoOpen {
		if err := openDesktopWindow(uiURL); err != nil {
			opts.Log.Warn("自动打开桌面控制台失败，请手动访问上方地址", "err", err)
		}
	}

	select {
	case <-runCtx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = httpSrv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}
