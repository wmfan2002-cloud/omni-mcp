package auth

import (
	"html/template"
	"net/http"
)

// 登录页与错误页。严格保持零外部资源引用（无外部字体、无外链脚本、无外链图片），
// 以满足 Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; form-action 'self' https:
// 并在反代后、内网或完全离线环境中稳定渲染。

type loginView struct {
	ReqID      string
	ClientName string
	Scope      string
	Username   string
	Error      string
}

type errorView struct {
	Title  string
	Detail string
}

const authLogoSVG = `<svg class="brand-logo" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none"> <defs> <linearGradient id="omni-ring" x1="8" y1="8" x2="56" y2="56" gradientUnits="userSpaceOnUse"> <stop offset="0%" stop-color="#6A0085" /> <stop offset="55%" stop-color="#9A24B8" /> <stop offset="100%" stop-color="#FF78C8" /> </linearGradient> <linearGradient id="omni-core" x1="22" y1="22" x2="42" y2="42" gradientUnits="userSpaceOnUse"> <stop offset="0%" stop-color="#0D0D0D" /> <stop offset="100%" stop-color="#3B074C" /> </linearGradient> </defs> <rect x="4" y="4" width="56" height="56" rx="16" fill="#0D0D0D" /> <circle cx="32" cy="32" r="19" stroke="url(#omni-ring)" stroke-width="3.2" stroke-linecap="round" stroke-dasharray="92 28" transform="rotate(-24 32 32)" /> <path d="M32 32 L18.5 23.5 M32 32 L45.5 23.5 M32 32 L32 48" stroke="#FFC9FF" stroke-width="2.4" stroke-linecap="round" opacity="0.88" /> <circle cx="18.5" cy="23.5" r="4.2" fill="#FFFFFF" /> <circle cx="45.5" cy="23.5" r="4.2" fill="#FFC9FF" /> <circle cx="32" cy="48" r="4.2" fill="#FFFFFF" /> <circle cx="32" cy="32" r="7.5" fill="url(#omni-core)" stroke="#FFFFFF" stroke-width="2.6" /> <circle cx="32" cy="32" r="2.4" fill="#FFC9FF" /> </svg>`

const pageStyle = `:root {
  color-scheme: dark;
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
  --font-sans: "Plus Jakarta Sans", -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei UI", "Segoe UI", system-ui, sans-serif;
  --font-mono: "IBM Plex Mono", ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { min-height: 100%; }
body {
  font-family: var(--font-sans);
  background-color: var(--bg);
  background-image: radial-gradient(760px 360px at 50% -100px, rgba(154, 36, 184, 0.16), transparent 72%);
  color: var(--fg);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 28px 18px;
  line-height: 1.55;
  -webkit-font-smoothing: antialiased;
}
.pass-shell {
  width: 100%;
  max-width: 880px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 16px;
  overflow: hidden;
  display: grid;
  grid-template-columns: 1.08fr 1fr;
}
.pass-manifest {
  padding: 34px 32px;
  background: var(--surface-alt);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 26px;
}
.brand-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.brand-left {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  text-decoration: none;
  color: var(--fg);
}
.brand-logo { width: 28px; height: 28px; flex-shrink: 0; }
.brand-name {
  font-size: 15px;
  font-weight: 700;
  letter-spacing: -0.02em;
}
.proto-spec {
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--accent-bright);
}
.manifest-kicker {
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--muted);
  margin-bottom: 8px;
}
.manifest-title {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.025em;
  color: var(--fg);
  line-height: 1.3;
  margin-bottom: 10px;
}
.manifest-desc {
  font-size: 13.5px;
  color: var(--text);
  line-height: 1.65;
}
.manifest-desc strong {
  color: var(--fg);
  font-weight: 600;
}
.topology-frame {
  padding: 14px 16px;
  background: var(--input-bg);
  border: 1px solid var(--border);
  border-radius: 10px;
}
.topo-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-family: var(--font-mono);
  font-size: 12px;
}
.topo-node {
  color: var(--fg);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 42%;
}
.topo-hub {
  color: var(--accent-bright);
  font-weight: 600;
  white-space: nowrap;
}
.topo-link {
  color: var(--muted);
  font-size: 11px;
  white-space: nowrap;
}
.topo-sub {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--muted);
}
.topo-sub code {
  color: var(--accent-bright);
  font-family: var(--font-mono);
}
.guard-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 4px;
  border-top: 1px solid var(--border);
}
.guard-item {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 12.5px;
  color: var(--text);
}
.guard-idx {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--accent-bright);
  flex-shrink: 0;
}
.pass-form {
  padding: 34px 32px;
  display: flex;
  flex-direction: column;
  justify-content: center;
}
.form-title {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--fg);
  margin-bottom: 6px;
}
.form-sub {
  font-size: 13px;
  color: var(--muted);
  margin-bottom: 22px;
}
.err {
  margin-bottom: 16px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  color: var(--danger);
  font-size: 12.5px;
  line-height: 1.5;
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
  font-family: var(--font-sans);
  outline: none;
  transition: border-color 0.15s ease;
}
input[type="text"]:focus,
input[type="password"]:focus {
  border-color: var(--accent);
}
button[type="submit"] {
  width: 100%;
  height: 42px;
  margin-top: 6px;
  border: none;
  border-radius: 9px;
  background: var(--cta-bg);
  color: var(--cta-fg);
  font-family: var(--font-sans);
  font-size: 13.5px;
  font-weight: 700;
  cursor: pointer;
  transition: background-color 0.15s ease, transform 0.1s ease;
}
button[type="submit"]:hover {
  background: var(--cta-hover);
}
button[type="submit"]:active {
  transform: translateY(1px);
}
.meta {
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
  font-size: 12px;
  color: var(--muted);
  line-height: 1.55;
}
.err-shell {
  width: 100%;
  max-width: 520px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 32px;
}
.back-link {
  display: inline-flex;
  align-items: center;
  margin-top: 20px;
  padding: 9px 16px;
  border-radius: 8px;
  border: 1px solid var(--border-strong);
  color: var(--fg);
  text-decoration: none;
  font-size: 13px;
  font-weight: 600;
  background: var(--surface-alt);
}
.back-link:hover {
  border-color: var(--accent);
  color: var(--accent-bright);
}
@media (max-width: 760px) {
  .pass-shell { grid-template-columns: 1fr; }
  .pass-manifest {
    border-right: none;
    border-bottom: 1px solid var(--border);
    padding: 24px 22px;
    gap: 18px;
  }
  .pass-form { padding: 24px 22px; }
}`

var loginTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>OAuth 2.1 授权核验 · omni-mcp</title>
<style>` + pageStyle + `</style>
</head>
<body>
<div class="pass-shell">
  <section class="pass-manifest" aria-label="客户端授权契约">
    <div class="brand-row">
      <a class="brand-left" href="/">` + authLogoSVG + `<span class="brand-name">omni-mcp</span></a>
      <span class="proto-spec">OAuth 2.1 · PKCE S256</span>
    </div>

    <div>
      <div class="manifest-kicker">授权协议契约 · RFC 7636</div>
      <h1 class="manifest-title">核准外部 AI 客户端接入工具枢纽</h1>
      <p class="manifest-desc">应用 <strong>{{.ClientName}}</strong> 正在申请通过加密会话调用此 MCP 枢纽上的跨端工具。</p>
    </div>

    <div class="topology-frame" aria-label="授权连接拓扑">
      <div class="topo-row">
        <span class="topo-node" title="{{.ClientName}}">{{.ClientName}}</span>
        <span class="topo-link">── S256 ──▶</span>
        <span class="topo-hub">omni-mcp Hub</span>
      </div>
      <div class="topo-sub">
        <span>授权范围 <code>{{.Scope}}</code></span>
        <span>·</span>
        <span>Streamable HTTP</span>
      </div>
    </div>

    <ul class="guard-list">
      <li class="guard-item"><span class="guard-idx">01.</span><span>严格限制在各节点显式挂载的沙箱工作目录内读写</span></li>
      <li class="guard-item"><span class="guard-idx">02.</span><span>破坏性终端指令由服务端策略引擎自动拦截并要求二次确认</span></li>
      <li class="guard-item"><span class="guard-idx">03.</span><span>每一次工具调用的参数摘要与耗时均写入审计日志</span></li>
    </ul>
  </section>

  <form class="pass-form" method="post" action="/oauth/login">
    <h2 class="form-title">管理员身份核验</h2>
    <p class="form-sub">请输入 Hub 管理员凭据以签发单次 PKCE 授权码</p>

    <input type="hidden" name="req_id" value="{{.ReqID}}">
    {{if .Error}}<div class="err" role="alert">{{.Error}}</div>{{end}}

    <div class="field">
      <label for="u">管理员用户名</label>
      <input id="u" name="username" type="text" autocomplete="username" autofocus required value="{{.Username}}">
    </div>
    <div class="field">
      <label for="p">登录密码</label>
      <input id="p" name="password" type="password" autocomplete="current-password" required>
    </div>

    <button type="submit">验证身份并签发授权</button>

    <p class="meta">若此次授权并非由你本人发起，请直接关闭当前标签页，任何凭证均不会被签发。</p>
  </form>
</div>
</body>
</html>`))

var errorTmpl = template.Must(template.New("err").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · omni-mcp</title>
<style>` + pageStyle + `</style>
</head>
<body>
<div class="err-shell" role="alert">
  <div class="brand-row" style="margin-bottom:20px">
    <div class="brand-left">` + authLogoSVG + `<span class="brand-name">omni-mcp</span></div>
    <span class="proto-spec">OAuth 2.1</span>
  </div>
  <h1 class="manifest-title">{{.Title}}</h1>
  <p class="manifest-desc">{{.Detail}}</p>
  <a class="back-link" href="/">← 返回 Hub 首页</a>
</div>
</body>
</html>`))

func (s *Server) renderLogin(w http.ResponseWriter, v loginView) {
	s.renderLoginWithStatus(w, v, http.StatusOK)
}

func (s *Server) renderLoginWithStatus(w http.ResponseWriter, v loginView, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' https:")
	w.WriteHeader(status)
	if err := loginTmpl.Execute(w, v); err != nil {
		s.log.Error("渲染登录页失败", "err", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := errorTmpl.Execute(w, errorView{Title: title, Detail: detail}); err != nil {
		s.log.Error("渲染错误页失败", "err", err)
	}
}
