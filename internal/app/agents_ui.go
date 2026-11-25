package app

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"omni-mcp/internal/agent"
	"omni-mcp/internal/auth"
	"omni-mcp/internal/config"
)

const portalCSRFCookie = "omni_portal_csrf"

//go:embed agents_portal.html
var agentsPortalHTML string

type agentsPortal struct {
	cfg    *config.Config
	auth   *auth.Server
	agents *agent.Manager
	log    *slog.Logger
}

func newAgentsPortal(cfg *config.Config, authSrv *auth.Server, agents *agent.Manager, log *slog.Logger) *agentsPortal {
	return &agentsPortal{
		cfg:    cfg,
		auth:   authSrv,
		agents: agents,
		log:    log,
	}
}

func (p *agentsPortal) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")

	path := strings.TrimSuffix(r.URL.Path, "/")
	switch path {
	case "/agents":
		p.handlePage(w, r)
	case "/agents/login":
		p.handleLogin(w, r)
	case "/agents/logout":
		p.handleLogout(w, r)
	case "/agents/api/state":
		p.handleState(w, r)
	case "/agents/api/approve":
		p.handleApprove(w, r)
	case "/agents/api/reject":
		p.handleReject(w, r)
	case "/agents/api/revoke":
		p.handleRevoke(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *agentsPortal) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := sanitizePairCode(r.URL.Query().Get("code"))
	sub, ok := p.auth.SessionSubject(r)
	if !ok {
		next := "/agents"
		if code != "" {
			next = "/agents?code=" + code
		}
		p.renderLogin(w, next, code, "")
		return
	}
	csrf := p.ensureCSRFCookie(w, r)
	page := strings.Replace(agentsPortalHTML, "__OMNI_PORTAL_CSRF__", csrf, 1)
	page = strings.Replace(page, "__OMNI_PORTAL_USER__", template.HTMLEscapeString(sub), 1)
	page = strings.Replace(page, "__OMNI_PORTAL_VERSION__", template.HTMLEscapeString(Version), 1)
	page = strings.Replace(page, "__OMNI_PORTAL_BASE__", template.HTMLEscapeString(p.cfg.Server.BaseURL), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func (p *agentsPortal) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		p.renderLogin(w, "/agents", "", "请求格式无效")
		return
	}
	next := sanitizePortalNext(r.PostFormValue("next"))
	code := sanitizePairCode(r.PostFormValue("code"))
	if code != "" && next == "/agents" {
		next = "/agents?code=" + code
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	if !p.auth.VerifyUser(username, password) {
		time.Sleep(250 * time.Millisecond)
		p.renderLogin(w, next, code, "用户名或密码不正确，请使用 Hub 管理员账号登录。")
		return
	}
	p.auth.IssueBrowserSession(w, r, username)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (p *agentsPortal) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.verifyCSRF(r) {
		writePortalError(w, http.StatusForbidden, "无效的会话令牌，请刷新页面后重试")
		return
	}
	p.auth.ClearBrowserSession(w, r)
	writePortalJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *agentsPortal) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sub, ok := p.auth.SessionSubject(r)
	if !ok {
		writePortalError(w, http.StatusUnauthorized, "登录已过期，请刷新页面重新登录")
		return
	}
	p.writeState(w, sub)
}

func (p *agentsPortal) handleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sub, ok := p.requireSessionAndCSRF(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "请求参数无效")
		return
	}
	if _, err := p.agents.ApprovePairing(req.Code, sub); err != nil {
		writePortalError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.writeState(w, sub)
}

func (p *agentsPortal) handleReject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sub, ok := p.requireSessionAndCSRF(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "请求参数无效")
		return
	}
	if err := p.agents.RejectPairing(req.Code); err != nil {
		writePortalError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.writeState(w, sub)
}

func (p *agentsPortal) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sub, ok := p.requireSessionAndCSRF(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "请求参数无效")
		return
	}
	if err := p.agents.RevokeDevice(req.Name); err != nil {
		writePortalError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.writeState(w, sub)
}

func (p *agentsPortal) writeState(w http.ResponseWriter, sub string) {
	writePortalJSON(w, http.StatusOK, map[string]any{
		"user":         sub,
		"auth_enabled": p.cfg.Auth.Enabled,
		"base_url":     p.cfg.Server.BaseURL,
		"version":      Version,
		"pending":      p.agents.ListPendingPairings(),
		"devices":      p.agents.ListDevices(),
	})
}

func (p *agentsPortal) requireSessionAndCSRF(w http.ResponseWriter, r *http.Request) (string, bool) {
	sub, ok := p.auth.SessionSubject(r)
	if !ok {
		writePortalError(w, http.StatusUnauthorized, "登录已过期，请刷新页面重新登录")
		return "", false
	}
	if !p.verifyCSRF(r) {
		writePortalError(w, http.StatusForbidden, "无效的 CSRF 令牌，请刷新页面后重试")
		return "", false
	}
	return sub, true
}

func (p *agentsPortal) ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(portalCSRFCookie); err == nil && len(c.Value) == 64 {
		return c.Value
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	tok := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name:     portalCSRFCookie,
		Value:    tok,
		Path:     "/agents",
		HttpOnly: true,
		Secure:   strings.HasPrefix(p.cfg.Server.BaseURL, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
	return tok
}

func (p *agentsPortal) verifyCSRF(r *http.Request) bool {
	c, err := r.Cookie(portalCSRFCookie)
	if err != nil || len(c.Value) != 64 {
		return false
	}
	hdr := strings.TrimSpace(r.Header.Get("X-Omni-CSRF"))
	return len(hdr) == len(c.Value) && subtle.ConstantTimeCompare([]byte(hdr), []byte(c.Value)) == 1
}

func sanitizePortalNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/agents") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "://") {
		return "/agents"
	}
	return raw
}

func sanitizePairCode(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if len(raw) != 9 || raw[4] != '-' {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		if i == 4 {
			continue
		}
		ch := raw[i]
		if !((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
			return ""
		}
	}
	return raw
}

type portalLoginData struct {
	Next    string
	Code    string
	Error   string
	Version string
}

var portalLoginTmpl = template.Must(template.New("portal_login").Parse(`<!doctype html>
<html lang="zh-CN" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>登录 · Omni MCP Hub 设备管理</title>
<script>
try {
  var t = localStorage.getItem('omni-theme');
  if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t);
} catch (e) {}
</script>
<style>
:root, [data-theme="dark"] {
  --bg: #090b0f; --surface: #11151d; --surface-2: #161b26;
  --border: rgba(255,255,255,0.09); --border-strong: rgba(255,255,255,0.16);
  --text: #eef1f6; --text-muted: #8b95a5;
  --accent: #3b82f6; --accent-hover: #2563eb; --accent-soft: rgba(59,130,246,0.14);
  --danger: #ef4444; --danger-soft: rgba(239,68,68,0.12);
  --shadow: 0 24px 64px rgba(0,0,0,0.5);
}
[data-theme="light"] {
  --bg: #f5f6f8; --surface: #ffffff; --surface-2: #f1f3f6;
  --border: rgba(15,23,42,0.09); --border-strong: rgba(15,23,42,0.16);
  --text: #0f172a; --text-muted: #64748b;
  --accent: #2563eb; --accent-hover: #1d4ed8; --accent-soft: rgba(37,99,235,0.1);
  --danger: #dc2626; --danger-soft: rgba(220,38,38,0.09);
  --shadow: 0 18px 48px rgba(15,23,42,0.08);
}
* { box-sizing: border-box; }
body {
  margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
  padding: 24px; background: var(--bg); color: var(--text);
  font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
}
.card {
  width: 100%; max-width: 400px; background: var(--surface);
  border: 1px solid var(--border); border-radius: 14px; padding: 28px;
  box-shadow: var(--shadow);
}
.brand { display: flex; align-items: center; gap: 10px; margin-bottom: 18px; }
.mark {
  width: 32px; height: 32px; border-radius: 8px; background: var(--accent);
  color: #fff; display: grid; place-items: center; font-weight: 700;
}
.brand-title { font-size: 16px; font-weight: 600; letter-spacing: -0.01em; }
.brand-sub { font-size: 12px; color: var(--text-muted); }
h1 { margin: 0 0 6px; font-size: 18px; font-weight: 600; }
.desc { margin: 0 0 18px; font-size: 13px; color: var(--text-muted); }
.code-banner {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 14px; margin-bottom: 18px; border-radius: 10px;
  background: var(--accent-soft); border: 1px solid rgba(59,130,246,0.28);
}
.code-label { font-size: 12px; color: var(--text-muted); }
.code-val { font: 700 18px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; letter-spacing: 0.08em; }
.error {
  padding: 10px 12px; margin-bottom: 16px; border-radius: 8px; font-size: 13px;
  background: var(--danger-soft); color: var(--danger); border: 1px solid rgba(239,68,68,0.25);
}
label { display: block; font-size: 12px; font-weight: 500; color: var(--text-muted); margin-bottom: 6px; }
.field { margin-bottom: 14px; }
input[type=text], input[type=password] {
  width: 100%; height: 38px; padding: 0 12px; border-radius: 8px;
  border: 1px solid var(--border-strong); background: var(--surface-2); color: var(--text);
  font: inherit; outline: none; transition: border-color 0.15s;
}
input:focus { border-color: var(--accent); }
button {
  width: 100%; height: 40px; margin-top: 6px; border: 0; border-radius: 8px;
  background: var(--accent); color: #fff; font: 600 14px/1 inherit; cursor: pointer;
  transition: background 0.15s;
}
button:hover { background: var(--accent-hover); }
.foot { margin-top: 16px; display: flex; justify-content: space-between; font-size: 12px; color: var(--text-muted); }
.foot a { color: var(--text-muted); text-decoration: none; }
.foot a:hover { color: var(--text); }
</style>
</head>
<body>
  <div class="card">
    <div class="brand">
      <div class="mark">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>
      </div>
      <div>
        <div class="brand-title">Omni MCP Hub</div>
        <div class="brand-sub">设备配对与接入中心 · {{.Version}}</div>
      </div>
    </div>
    <h1>管理员登录</h1>
    <p class="desc">请使用 Hub 配置的管理员账号登录，以批准新设备配对或管理已接入节点。</p>
    {{if .Code}}
    <div class="code-banner">
      <div>
        <div class="code-label">待核对的配对验证码</div>
        <div class="code-val">{{.Code}}</div>
      </div>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
    </div>
    {{end}}
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <form method="post" action="/agents/login">
      <input type="hidden" name="next" value="{{.Next}}">
      <input type="hidden" name="code" value="{{.Code}}">
      <div class="field">
        <label for="username">用户名</label>
        <input id="username" name="username" type="text" autocomplete="username" required autofocus>
      </div>
      <div class="field">
        <label for="password">密码</label>
        <input id="password" name="password" type="password" autocomplete="current-password" required>
      </div>
      <button type="submit">登录并继续</button>
    </form>
    <div class="foot">
      <a href="/">← 返回 Hub 首页</a>
      <span>会话Cookie已启用 HttpOnly</span>
    </div>
  </div>
</body>
</html>`))

func (p *agentsPortal) renderLogin(w http.ResponseWriter, next, code, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = portalLoginTmpl.Execute(w, portalLoginData{
		Next:    next,
		Code:    code,
		Error:   errMsg,
		Version: Version,
	})
}

func writePortalJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writePortalError(w http.ResponseWriter, status int, msg string) {
	writePortalJSON(w, status, map[string]string{"error": msg})
}
