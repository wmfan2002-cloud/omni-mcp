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
	baseURL := strings.TrimRight(strings.TrimSpace(p.cfg.Server.BaseURL), "/")
	if baseURL == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		if r.Host != "" {
			baseURL = scheme + "://" + r.Host
		}
	}
	page := strings.ReplaceAll(agentsPortalHTML, "__OMNI_PORTAL_CSRF__", csrf)
	page = strings.ReplaceAll(page, "__OMNI_PORTAL_USER__", template.HTMLEscapeString(sub))
	page = strings.ReplaceAll(page, "__OMNI_PORTAL_VERSION__", template.HTMLEscapeString(Version))
	page = strings.ReplaceAll(page, "__OMNI_PORTAL_BASE__", template.HTMLEscapeString(baseURL))
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
<title>管理员核验 · 设备管理中心 · omni-mcp</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%20viewBox%3D%220%200%2064%2064%22%20fill%3D%22none%22%3E%3Cdefs%3E%3ClinearGradient%20id%3D%22omni-ring%22%20x1%3D%228%22%20y1%3D%228%22%20x2%3D%2256%22%20y2%3D%2256%22%20gradientUnits%3D%22userSpaceOnUse%22%3E%3Cstop%20offset%3D%220%25%22%20stop-color%3D%22%236A0085%22%20%2F%3E%3Cstop%20offset%3D%2255%25%22%20stop-color%3D%22%239A24B8%22%20%2F%3E%3Cstop%20offset%3D%22100%25%22%20stop-color%3D%22%23FF78C8%22%20%2F%3E%3C%2FlinearGradient%3E%3ClinearGradient%20id%3D%22omni-core%22%20x1%3D%2222%22%20y1%3D%2222%22%20x2%3D%2242%22%20y2%3D%2242%22%20gradientUnits%3D%22userSpaceOnUse%22%3E%3Cstop%20offset%3D%220%25%22%20stop-color%3D%22%230D0D0D%22%20%2F%3E%3Cstop%20offset%3D%22100%25%22%20stop-color%3D%22%233B074C%22%20%2F%3E%3C%2FlinearGradient%3E%3C%2Fdefs%3E%3Crect%20x%3D%224%22%20y%3D%224%22%20width%3D%2256%22%20height%3D%2256%22%20rx%3D%2216%22%20fill%3D%22%230D0D0D%22%20%2F%3E%3Ccircle%20cx%3D%2232%22%20cy%3D%2232%22%20r%3D%2219%22%20stroke%3D%22url(%23omni-ring)%22%20stroke-width%3D%223.2%22%20stroke-linecap%3D%22round%22%20stroke-dasharray%3D%2292%2028%22%20transform%3D%22rotate(-24%2032%2032)%22%20%2F%3E%3Cpath%20d%3D%22M32%2032%20L18.5%2023.5%20M32%2032%20L45.5%2023.5%20M32%2032%20L32%2048%22%20stroke%3D%22%23FFC9FF%22%20stroke-width%3D%222.4%22%20stroke-linecap%3D%22round%22%20opacity%3D%220.88%22%20%2F%3E%3Ccircle%20cx%3D%2218.5%22%20cy%3D%2223.5%22%20r%3D%224.2%22%20fill%3D%22%23FFFFFF%22%20%2F%3E%3Ccircle%20cx%3D%2245.5%22%20cy%3D%2223.5%22%20r%3D%224.2%22%20fill%3D%22%23FFC9FF%22%20%2F%3E%3Ccircle%20cx%3D%2232%22%20cy%3D%2248%22%20r%3D%224.2%22%20fill%3D%22%23FFFFFF%22%20%2F%3E%3Ccircle%20cx%3D%2232%22%20cy%3D%2232%22%20r%3D%227.5%22%20fill%3D%22url(%23omni-core)%22%20stroke%3D%22%23FFFFFF%22%20stroke-width%3D%222.6%22%20%2F%3E%3Ccircle%20cx%3D%2232%22%20cy%3D%2232%22%20r%3D%222.4%22%20fill%3D%22%23FFC9FF%22%20%2F%3E%3C%2Fsvg%3E">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600&family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
<script>
  (function () {
    try {
      var saved = localStorage.getItem('omni-theme') || localStorage.getItem('omni-steep-theme');
      if (saved === 'light' || saved === 'dark') {
        document.documentElement.setAttribute('data-theme', saved);
      } else {
        document.documentElement.setAttribute('data-theme', 'dark');
      }
    } catch (e) {}
  })();
</script>
<style>
  :root, [data-theme="dark"] {
    --bg: #09090b;
    --surface: #111115;
    --surface-alt: #18181d;
    --input-bg: #09090c;
    --border: rgba(255, 255, 255, 0.08);
    --border-strong: rgba(255, 255, 255, 0.16);
    --fg: #fafafa;
    --text: #a1a1aa;
    --muted: #71717a;
    --cta-bg: #fafafa;
    --cta-hover: #e4e4e7;
    --cta-fg: #09090b;
    --accent: #c084fc;
    --accent-bright: #d8b4fe;
    --accent-ink: #09090b;
    --accent-soft: rgba(192, 132, 252, 0.09);
    --accent-border: rgba(192, 132, 252, 0.26);
    --danger: #f87171;
    --danger-bg: rgba(239, 68, 68, 0.10);
    --danger-border: rgba(239, 68, 68, 0.28);
    --glow: rgba(154, 36, 184, 0.16);
  }
  [data-theme="light"] {
    --bg: #fafafa;
    --surface: #ffffff;
    --surface-alt: #f4f4f5;
    --input-bg: #f8f8fa;
    --border: #e4e4e7;
    --border-strong: #d4d4d8;
    --fg: #09090b;
    --text: #3f3f46;
    --muted: #71717a;
    --cta-bg: #09090b;
    --cta-hover: #27272a;
    --cta-fg: #ffffff;
    --accent: #9333ea;
    --accent-bright: #7e22ce;
    --accent-ink: #ffffff;
    --accent-soft: rgba(147, 51, 234, 0.07);
    --accent-border: rgba(147, 51, 234, 0.24);
    --danger: #dc2626;
    --danger-bg: rgba(220, 38, 38, 0.08);
    --danger-border: rgba(220, 38, 38, 0.26);
    --glow: rgba(154, 36, 184, 0.07);
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body { min-height: 100%; }
  body {
    font-family: "Plus Jakarta Sans", -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei UI", "Segoe UI", sans-serif;
    background-color: var(--bg);
    background-image: radial-gradient(880px 360px at 50% -110px, var(--glow), transparent 72%);
    color: var(--fg);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 28px 18px;
    line-height: 1.55;
    -webkit-font-smoothing: antialiased;
  }
  .mono {
    font-family: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-variant-numeric: tabular-nums;
  }
  .gate-shell {
    width: 100%;
    max-width: 880px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 16px;
    overflow: hidden;
    display: grid;
    grid-template-columns: 1.08fr 1fr;
  }
  .gate-left {
    padding: 34px 32px;
    background: var(--surface-alt);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    gap: 24px;
  }
  .brand-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .brand-link {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    text-decoration: none;
    color: var(--fg);
  }
  .brand-link svg { width: 28px; height: 28px; flex-shrink: 0; }
  .brand-word {
    font-size: 15px;
    font-weight: 700;
    letter-spacing: -0.02em;
  }
  .theme-btn {
    width: 34px;
    height: 34px;
    border-radius: 8px;
    border: 1px solid var(--border-strong);
    background: var(--surface);
    color: var(--text);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    transition: border-color 0.15s ease, color 0.15s ease;
  }
  .theme-btn:hover { border-color: var(--accent); color: var(--fg); }
  .kicker {
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 8px;
  }
  .left-title {
    font-size: 22px;
    font-weight: 700;
    letter-spacing: -0.025em;
    line-height: 1.3;
    margin-bottom: 10px;
    text-wrap: balance;
  }
  .left-desc {
    font-size: 13.5px;
    color: var(--text);
    line-height: 1.65;
  }
  .ticket-box {
    padding: 16px 18px;
    border-radius: 12px;
    background: var(--input-bg);
    border: 1px solid var(--accent-border);
  }
  .ticket-lbl {
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 6px;
  }
  .ticket-code {
    font-size: 26px;
    font-weight: 600;
    letter-spacing: 0.08em;
    color: var(--accent-bright);
  }
  .ticket-sub {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
    font-size: 12px;
    color: var(--muted);
  }
  .arch-box {
    padding: 14px 16px;
    border-radius: 10px;
    background: var(--input-bg);
    border: 1px solid var(--border);
    font-size: 12px;
    color: var(--text);
  }
  .arch-flow {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 8px;
    color: var(--fg);
    font-weight: 600;
  }
  .arch-arrow { color: var(--accent-bright); }
  .feature-points {
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
    font-size: 12.5px;
    color: var(--text);
  }
  .feature-points li {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }
  .pt-num { color: var(--accent-bright); font-size: 11px; flex-shrink: 0; }
  .gate-right {
    padding: 34px 32px;
    display: flex;
    flex-direction: column;
    justify-content: center;
  }
  .form-head {
    font-size: 18px;
    font-weight: 700;
    letter-spacing: -0.02em;
    margin-bottom: 6px;
  }
  .form-sub {
    font-size: 13px;
    color: var(--muted);
    margin-bottom: 22px;
  }
  .error {
    margin-bottom: 16px;
    padding: 10px 12px;
    border-radius: 8px;
    background: var(--danger-bg);
    border: 1px solid var(--danger-border);
    color: var(--danger);
    font-size: 12.5px;
  }
  .field { margin-bottom: 16px; }
  label {
    display: block;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--text);
    margin-bottom: 6px;
  }
  input[type="text"],
  input[type="password"] {
    width: 100%;
    height: 42px;
    padding: 0 13px;
    border-radius: 9px;
    border: 1px solid var(--border-strong);
    background: var(--input-bg);
    color: var(--fg);
    font-size: 14px;
    font-family: inherit;
    outline: none;
    transition: border-color 0.15s ease;
  }
  input:focus { border-color: var(--accent); }
  .submit-btn {
    width: 100%;
    height: 42px;
    margin-top: 6px;
    border: none;
    border-radius: 9px;
    background: var(--cta-bg);
    color: var(--cta-fg);
    font-family: inherit;
    font-size: 13.5px;
    font-weight: 700;
    cursor: pointer;
    transition: opacity 0.15s ease, transform 0.1s ease;
  }
  .submit-btn:hover { opacity: 0.92; }
  .submit-btn:active { transform: translateY(1px); }
  .foot {
    margin-top: 20px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    font-size: 12px;
    color: var(--muted);
  }
  .foot a {
    color: var(--text);
    text-decoration: none;
    font-weight: 500;
  }
  .foot a:hover { color: var(--accent-bright); }
  @media (max-width: 760px) {
    .gate-shell { grid-template-columns: 1fr; }
    .gate-left {
      border-right: none;
      border-bottom: 1px solid var(--border);
      padding: 24px 22px;
    }
    .gate-right { padding: 24px 22px; }
  }
</style>
</head>
<body>
  <div class="gate-shell">
    <section class="gate-left" aria-label="设备管理中心说明">
      <div class="brand-bar">
        <a href="/" class="brand-link">
          <svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none"> <defs> <linearGradient id="omni-ring" x1="8" y1="8" x2="56" y2="56" gradientUnits="userSpaceOnUse"> <stop offset="0%" stop-color="#6A0085" /> <stop offset="55%" stop-color="#9A24B8" /> <stop offset="100%" stop-color="#FF78C8" /> </linearGradient> <linearGradient id="omni-core" x1="22" y1="22" x2="42" y2="42" gradientUnits="userSpaceOnUse"> <stop offset="0%" stop-color="#0D0D0D" /> <stop offset="100%" stop-color="#3B074C" /> </linearGradient> </defs> <rect x="4" y="4" width="56" height="56" rx="16" fill="#0D0D0D" /> <circle cx="32" cy="32" r="19" stroke="url(#omni-ring)" stroke-width="3.2" stroke-linecap="round" stroke-dasharray="92 28" transform="rotate(-24 32 32)" /> <path d="M32 32 L18.5 23.5 M32 32 L45.5 23.5 M32 32 L32 48" stroke="#FFC9FF" stroke-width="2.4" stroke-linecap="round" opacity="0.88" /> <circle cx="18.5" cy="23.5" r="4.2" fill="#FFFFFF" /> <circle cx="45.5" cy="23.5" r="4.2" fill="#FFC9FF" /> <circle cx="32" cy="48" r="4.2" fill="#FFFFFF" /> <circle cx="32" cy="32" r="7.5" fill="url(#omni-core)" stroke="#FFFFFF" stroke-width="2.6" /> <circle cx="32" cy="32" r="2.4" fill="#FFC9FF" /> </svg>
          <span class="brand-word">omni-mcp</span>
        </a>
        <button type="button" class="theme-btn" id="themeBtn" aria-label="切换深浅主题" title="切换深浅主题">
          <svg id="themeIcon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"></svg>
        </button>
      </div>

      <div>
        <div class="kicker mono">设备管理中心 · {{.Version}}</div>
        <h1 class="left-title">跨平台节点配对与机群访问控制</h1>
        <p class="left-desc">通过反向 WebSocket 安全隧道统一纳管 Linux 服务器、macOS 与 Windows 个人电脑，无需公网 IP 或开放入站端口。</p>
      </div>

      {{if .Code}}
      <div class="ticket-box">
        <div class="ticket-lbl">待核准的设备配对码</div>
        <div class="ticket-code mono">{{.Code}}</div>
        <div class="ticket-sub">登录后将直接定位至该配对请求，请核对与终端屏幕显示的八位码一致。</div>
      </div>
      {{else}}
      <div class="arch-box">
        <div class="arch-flow mono">
          <span>Desktop / CLI Agent</span>
          <span class="arch-arrow">── WSS ──▶</span>
          <span>Hub /agents</span>
        </div>
        <div style="color:var(--muted)">每台接入节点分配独立随机密钥，支持随时单点吊销并即时切断底层长连接。</div>
      </div>
      {{end}}

      <ul class="feature-points">
        <li><span class="pt-num mono">01.</span><span>八位配对码零 SSH 免密签发，本机自动存入系统安全钥匙串</span></li>
        <li><span class="pt-num mono">02.</span><span>实时查看机群在线心跳、挂载工作区目录与终端执行开关</span></li>
        <li><span class="pt-num mono">03.</span><span>支持一键吊销失信设备凭证，毫秒级终止正在运行的隧道</span></li>
      </ul>
    </section>

    <div class="gate-right">
      <h2 class="form-head">管理员身份验证</h2>
      <p class="form-sub">使用 Hub 管理员账号登录设备管理中心</p>

      {{if .Error}}<div class="error" role="alert">{{.Error}}</div>{{end}}

      <form method="post" action="/agents/login">
        <input type="hidden" name="next" value="{{.Next}}">
        <input type="hidden" name="code" value="{{.Code}}">
        <div class="field">
          <label for="username">管理员用户名</label>
          <input id="username" name="username" type="text" autocomplete="username" required autofocus>
        </div>
        <div class="field">
          <label for="password">登录密码</label>
          <input id="password" name="password" type="password" autocomplete="current-password" required>
        </div>
        <button type="submit" class="submit-btn">登录并进入设备管理中心</button>
      </form>

      <div class="foot">
        <a href="/">← 返回 Hub 首页</a>
        <span class="mono">HttpOnly · SameSite=Strict</span>
      </div>
    </div>
  </div>
  <script>
    (function () {
      function syncIcon() {
        var dark = document.documentElement.getAttribute("data-theme") === "dark";
        var el = document.getElementById("themeIcon");
        if (!el) return;
        el.innerHTML = dark
          ? '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>'
          : '<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>';
      }
      var btn = document.getElementById("themeBtn");
      if (btn) {
        btn.onclick = function () {
          var next = document.documentElement.getAttribute("data-theme") === "dark" ? "light" : "dark";
          document.documentElement.setAttribute("data-theme", next);
          try {
            localStorage.setItem("omni-theme", next);
            localStorage.setItem("omni-steep-theme", next);
          } catch (e) {}
          syncIcon();
        };
      }
      syncIcon();
    })();
  </script>
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
