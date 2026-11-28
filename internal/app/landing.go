package app

import (
	"encoding/json"
	"html/template"
	"net/http"

	"omni-mcp/internal/config"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type landingData struct {
	BaseURL   string
	MCPURL    string
	SourceURL string
	Tools     []string
	AuthOn    bool
	Version   string
	ToolCount int
}

// 打开根路径时提供统一的基础设施总览、实时拓扑探测、接入工作台与已注册工具规范目录。
var landingTmpl = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="zh-CN" data-theme="dark">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>omni-mcp · 跨云端与个人终端的自托管 MCP 控制平面</title>
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
      --surface-code: #09090c;
      --header-bg: rgba(9, 9, 11, 0.84);
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
      --ok: #34d399;
      --warn: #fbbf24;
      --danger: #f87171;
      --glow: rgba(154, 36, 184, 0.16);
      --code-fg: #e4e4e7;
      --font-sans: "Plus Jakarta Sans", -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei UI", "Segoe UI", sans-serif;
      --font-mono: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }

    [data-theme="light"] {
      --bg: #fafafa;
      --surface: #ffffff;
      --surface-alt: #f4f4f5;
      --surface-code: #f8f8fa;
      --header-bg: rgba(250, 250, 250, 0.88);
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
      --ok: #16a34a;
      --warn: #d97706;
      --danger: #dc2626;
      --glow: rgba(154, 36, 184, 0.07);
      --code-fg: #18181b;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    html { scroll-behavior: smooth; }
    body {
      font-family: var(--font-sans);
      background-color: var(--bg);
      background-image: radial-gradient(980px 380px at 50% -120px, var(--glow), transparent 72%);
      color: var(--fg);
      line-height: 1.6;
      -webkit-font-smoothing: antialiased;
    }
    .mono {
      font-family: var(--font-mono);
      font-variant-numeric: tabular-nums;
    }
    .container {
      max-width: 1180px;
      margin: 0 auto;
      padding: 0 24px;
    }

    /* Strict 3-Zone Top Bar */
    .topbar {
      position: sticky;
      top: 0;
      z-index: 30;
      background: var(--header-bg);
      backdrop-filter: blur(12px);
      -webkit-backdrop-filter: blur(12px);
      border-bottom: 1px solid var(--border);
    }
    .topbar-inner {
      max-width: 1180px;
      margin: 0 auto;
      padding: 0 24px;
      height: 60px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 32px;
    }
    .brand-mark {
      font-size: 16px;
      font-weight: 700;
      letter-spacing: -0.025em;
      color: var(--fg);
      text-decoration: none;
      white-space: nowrap;
      flex-shrink: 0;
    }
    .topnav {
      display: flex;
      align-items: center;
      gap: 28px;
    }
    .topnav a {
      font-size: 13.5px;
      font-weight: 500;
      color: var(--text);
      text-decoration: none;
      white-space: nowrap;
      flex-shrink: 0;
      transition: color 0.15s ease;
    }
    .topnav a:hover {
      color: var(--fg);
      text-decoration: underline;
      text-underline-offset: 5px;
    }
    .top-cta {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      height: 36px;
      padding: 0 15px;
      border-radius: 8px;
      background: var(--cta-bg);
      color: var(--cta-fg);
      font-size: 13px;
      font-weight: 700;
      text-decoration: none;
      white-space: nowrap;
      flex-shrink: 0;
      transition: opacity 0.15s ease;
    }
    .top-cta:hover { opacity: 0.9; }

    /* Hero Section */
    .hero {
      padding: 52px 0 36px;
    }
    .hero-utility {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 24px;
    }
    .hero-meta-line {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px;
      font-size: 12.5px;
      color: var(--muted);
    }
    .hero-meta-ok { color: var(--ok); font-weight: 600; }
    .hero-tools-right {
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .util-btn {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      height: 32px;
      padding: 0 12px;
      border-radius: 8px;
      border: 1px solid var(--border);
      background: var(--surface);
      color: var(--text);
      font-family: inherit;
      font-size: 12px;
      font-weight: 500;
      text-decoration: none;
      cursor: pointer;
      white-space: nowrap;
      transition: border-color 0.15s ease, color 0.15s ease;
    }
    .util-btn:hover { border-color: var(--border-strong); color: var(--fg); }

    .hero-grid {
      display: grid;
      grid-template-columns: 1.05fr 0.95fr;
      gap: 28px;
      align-items: stretch;
    }
    .hero-copy {
      display: flex;
      flex-direction: column;
      justify-content: space-between;
      gap: 24px;
      padding: 8px 0;
    }
    .hero-title {
      font-size: clamp(2.15rem, 3.8vw, 3.35rem);
      font-weight: 700;
      letter-spacing: -0.032em;
      line-height: 1.15;
      color: var(--fg);
      text-wrap: balance;
      margin-bottom: 18px;
    }
    .hero-lead {
      font-size: 15.5px;
      color: var(--text);
      line-height: 1.7;
      max-width: 58ch;
    }
    .hero-actions {
      display: flex;
      flex-direction: column;
      gap: 14px;
    }
    .endpoint-bar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      padding: 8px 10px 8px 14px;
      border-radius: 10px;
      background: var(--surface);
      border: 1px solid var(--border-strong);
    }
    .endpoint-bar-left {
      min-width: 0;
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .endpoint-tag {
      font-size: 11px;
      font-weight: 600;
      color: var(--accent-bright);
      white-space: nowrap;
    }
    .endpoint-url {
      font-size: 13px;
      color: var(--fg);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .copy-btn {
      height: 34px;
      padding: 0 14px;
      border-radius: 7px;
      border: 1px solid var(--border-strong);
      background: var(--surface-alt);
      color: var(--fg);
      font-family: inherit;
      font-size: 12.5px;
      font-weight: 600;
      cursor: pointer;
      white-space: nowrap;
      flex-shrink: 0;
      transition: border-color 0.15s ease, color 0.15s ease;
    }
    .copy-btn:hover { border-color: var(--accent); color: var(--accent-bright); }
    .cta-row {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 12px;
    }
    .btn-primary {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      height: 42px;
      padding: 0 20px;
      border-radius: 9px;
      background: var(--cta-bg);
      color: var(--cta-fg);
      font-size: 13.5px;
      font-weight: 700;
      text-decoration: none;
      white-space: nowrap;
      border: none;
      cursor: pointer;
      transition: opacity 0.15s ease;
    }
    .btn-primary:hover { opacity: 0.92; }
    .btn-secondary {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      height: 42px;
      padding: 0 18px;
      border-radius: 9px;
      background: var(--surface);
      border: 1px solid var(--border-strong);
      color: var(--fg);
      font-size: 13.5px;
      font-weight: 600;
      text-decoration: none;
      white-space: nowrap;
      cursor: pointer;
      transition: border-color 0.15s ease;
    }
    .btn-secondary:hover { border-color: var(--accent); }

    /* Right Hero Blueprint Console */
    .blueprint-panel {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 14px;
      display: flex;
      flex-direction: column;
      justify-content: space-between;
      overflow: hidden;
    }
    .bp-head {
      padding: 13px 18px;
      background: var(--surface-alt);
      border-bottom: 1px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      font-size: 12px;
    }
    .bp-title {
      font-weight: 600;
      color: var(--fg);
    }
    .bp-probe-btn {
      height: 28px;
      padding: 0 11px;
      border-radius: 6px;
      border: 1px solid var(--accent-border);
      background: var(--accent-soft);
      color: var(--accent-bright);
      font-family: inherit;
      font-size: 11.5px;
      font-weight: 600;
      cursor: pointer;
      white-space: nowrap;
      transition: background-color 0.15s ease;
    }
    .bp-probe-btn:hover { background: rgba(16, 185, 129, 0.18); }
    .bp-diagram {
      padding: 20px 20px 16px;
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .topo-stage {
      padding: 12px 14px;
      border-radius: 10px;
      background: var(--surface-alt);
      border: 1px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }
    .topo-stage.is-hub {
      border-color: var(--accent-border);
      background: var(--accent-soft);
    }
    .stage-name {
      font-size: 13px;
      font-weight: 700;
      color: var(--fg);
    }
    .stage-sub {
      font-size: 11.5px;
      color: var(--muted);
    }
    .stage-right {
      font-size: 11.5px;
      color: var(--accent-bright);
      text-align: right;
      white-space: nowrap;
    }
    .topo-connector {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      font-size: 11px;
      color: var(--muted);
    }
    .bp-readout {
      padding: 12px 18px;
      background: var(--surface-code);
      border-top: 1px solid var(--border);
      color: var(--code-fg);
      font-size: 11.5px;
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
    }
    .bp-readout-ok { color: #34d399; }

    /* 4-Column Telemetry Strip */
    .kpi-strip {
      margin-top: 26px;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 14px;
      display: grid;
      grid-template-columns: repeat(4, 1fr);
    }
    .kpi-cell {
      padding: 18px 20px;
      border-right: 1px solid var(--border);
    }
    .kpi-cell:last-child { border-right: none; }
    .kpi-label {
      font-size: 12px;
      color: var(--muted);
      margin-bottom: 6px;
    }
    .kpi-val {
      font-size: 16px;
      font-weight: 700;
      color: var(--fg);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .kpi-sub {
      margin-top: 4px;
      font-size: 11.5px;
      color: var(--muted);
    }

    /* Section Headers */
    .section {
      padding: 52px 0 12px;
    }
    .sec-head {
      display: flex;
      flex-wrap: wrap;
      align-items: flex-end;
      justify-content: space-between;
      gap: 16px;
      margin-bottom: 22px;
    }
    .sec-title {
      font-size: 24px;
      font-weight: 700;
      letter-spacing: -0.025em;
      color: var(--fg);
    }
    .sec-desc {
      font-size: 14px;
      color: var(--muted);
      max-width: 60ch;
      margin-top: 4px;
    }

    /* Asymmetric Bento Grid */
    .bento-grid {
      display: grid;
      grid-template-columns: repeat(12, 1fr);
      gap: 18px;
    }
    .bento-card {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 14px;
      padding: 24px;
      display: flex;
      flex-direction: column;
      justify-content: space-between;
      gap: 18px;
    }
    .span-7 { grid-column: span 7; }
    .span-5 { grid-column: span 5; }
    .bento-num {
      font-size: 12px;
      font-weight: 600;
      color: var(--accent-bright);
      margin-bottom: 6px;
    }
    .bento-title {
      font-size: 17px;
      font-weight: 700;
      letter-spacing: -0.02em;
      color: var(--fg);
      margin-bottom: 8px;
    }
    .bento-body {
      font-size: 13.5px;
      color: var(--text);
      line-height: 1.65;
    }
    .bento-spec {
      padding-top: 14px;
      border-top: 1px solid var(--border);
      display: flex;
      flex-direction: column;
      gap: 7px;
      font-size: 12px;
    }
    .spec-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }
    .spec-allow { color: var(--accent-bright); }
    .spec-deny { color: var(--danger); }

    /* Interactive Workbench (#quickstart) */
    .workbench {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 14px;
      overflow: hidden;
      display: grid;
      grid-template-columns: 290px 1fr;
    }
    .wb-rail {
      background: var(--surface-alt);
      border-right: 1px solid var(--border);
      padding: 14px;
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .wb-tab {
      width: 100%;
      text-align: left;
      padding: 11px 13px;
      border-radius: 9px;
      border: 1px solid transparent;
      background: transparent;
      color: var(--text);
      font-family: inherit;
      cursor: pointer;
      transition: background-color 0.15s ease, color 0.15s ease, border-color 0.15s ease;
    }
    .wb-tab:hover { color: var(--fg); background: var(--surface); }
    .wb-tab.active {
      background: var(--surface);
      border-color: var(--accent-border);
      color: var(--fg);
    }
    .wb-tab-title {
      display: block;
      font-size: 13px;
      font-weight: 600;
      margin-bottom: 2px;
    }
    .wb-tab-sub {
      display: block;
      font-size: 11.5px;
      color: var(--muted);
    }
    .wb-stage {
      background: var(--surface-code);
      color: var(--code-fg);
      display: flex;
      flex-direction: column;
      justify-content: space-between;
      min-width: 0;
    }
    .wb-stage-head {
      padding: 12px 20px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.08);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      font-size: 12px;
      color: #94a3b8;
    }
    .wb-copy-code {
      height: 28px;
      padding: 0 12px;
      border-radius: 6px;
      border: 1px solid rgba(255, 255, 255, 0.16);
      background: rgba(255, 255, 255, 0.06);
      color: #f8fafc;
      font-family: inherit;
      font-size: 12px;
      font-weight: 600;
      cursor: pointer;
      white-space: nowrap;
    }
    .wb-copy-code:hover { border-color: var(--border-strong); color: var(--fg); }
    .wb-pre {
      padding: 20px;
      font-size: 12.5px;
      line-height: 1.7;
      overflow-x: auto;
      white-space: pre;
      color: #e2e8f0;
      flex: 1;
    }
    .wb-foot {
      padding: 11px 20px;
      border-top: 1px solid rgba(255, 255, 255, 0.08);
      font-size: 12px;
      color: #94a3b8;
    }

    /* Tool Matrix (#tools) */
    .tool-panel {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: 14px;
      overflow: hidden;
    }
    .tool-toolbar {
      padding: 14px 20px;
      background: var(--surface-alt);
      border-bottom: 1px solid var(--border);
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }
    .seg-bar {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      padding: 3px;
      border-radius: 8px;
      background: var(--bg);
      border: 1px solid var(--border);
    }
    .seg-btn {
      height: 28px;
      padding: 0 12px;
      border-radius: 6px;
      border: none;
      background: transparent;
      color: var(--muted);
      font-family: inherit;
      font-size: 12px;
      font-weight: 600;
      cursor: pointer;
      white-space: nowrap;
    }
    .seg-btn.active {
      background: var(--surface);
      color: var(--fg);
    }
    .tool-search {
      height: 34px;
      width: 240px;
      padding: 0 12px;
      border-radius: 8px;
      border: 1px solid var(--border-strong);
      background: var(--bg);
      color: var(--fg);
      font-family: inherit;
      font-size: 12.5px;
      outline: none;
    }
    .tool-search:focus { border-color: var(--accent); }
    .tool-grid {
      display: grid;
      grid-template-columns: repeat(2, 1fr);
    }
    .tool-cell {
      padding: 18px 22px;
      border-right: 1px solid var(--border);
      border-bottom: 1px solid var(--border);
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .tool-cell:nth-child(2n) { border-right: none; }
    .tool-top {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 12px;
    }
    .tool-name {
      font-size: 14px;
      font-weight: 600;
      color: var(--accent-bright);
    }
    .tool-cat {
      font-size: 11.5px;
      color: var(--muted);
    }
    .tool-desc {
      font-size: 13px;
      color: var(--text);
      line-height: 1.55;
    }
    .tool-sig {
      font-size: 11.5px;
      color: var(--muted);
      margin-top: 2px;
    }
    .tool-empty {
      padding: 36px 20px;
      text-align: center;
      font-size: 13.5px;
      color: var(--muted);
    }

    /* Footer */
    .footer {
      margin-top: 56px;
      border-top: 1px solid var(--border);
      padding: 24px 0 36px;
      font-size: 12.5px;
      color: var(--muted);
    }
    .footer-inner {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
    }
    .footer-links {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 18px;
    }
    .footer-links a {
      color: var(--text);
      text-decoration: none;
    }
    .footer-links a:hover { color: var(--accent-bright); }

    @media (max-width: 960px) {
      .hero-grid { grid-template-columns: 1fr; }
      .kpi-strip { grid-template-columns: repeat(2, 1fr); }
      .kpi-cell:nth-child(2) { border-right: none; }
      .kpi-cell:nth-child(1), .kpi-cell:nth-child(2) { border-bottom: 1px solid var(--border); }
      .span-7, .span-5 { grid-column: span 12; }
      .workbench { grid-template-columns: 1fr; }
      .wb-rail {
        border-right: none;
        border-bottom: 1px solid var(--border);
        flex-direction: row;
        overflow-x: auto;
      }
      .wb-tab { min-width: 190px; }
      .tool-grid { grid-template-columns: 1fr; }
      .tool-cell { border-right: none; }
    }
    @media (max-width: 720px) {
      .topnav { display: none; }
      .kpi-strip { grid-template-columns: 1fr; }
      .kpi-cell { border-right: none; border-bottom: 1px solid var(--border); }
      .kpi-cell:last-child { border-bottom: none; }
      .tool-search { width: 100%; }
    }
  </style>
</head>
<body>
  <!-- Strict 3-Zone Top Bar -->
  <header class="topbar">
    <div class="topbar-inner">
      <a href="/" class="brand-mark">omni-mcp</a>

      <nav class="topnav" aria-label="主导航">
        <a href="#architecture">架构拓扑</a>
        <a href="#guardrails">安全护栏</a>
        <a href="#quickstart">接入工作台</a>
        <a href="#tools">工具规范</a>
      </nav>

      <a href="/agents" class="top-cta">进入设备管理中心 →</a>
    </div>
  </header>

  <main class="container">
    <!-- Hero Section -->
    <section class="hero" id="architecture">
      <div class="hero-utility">
        <div class="hero-meta-line mono">
          <span id="liveHealthBadge" class="hero-meta-ok">Hub 在线运行中</span>
          <span aria-hidden="true">·</span>
          <span>版本 {{.Version}}</span>
          <span aria-hidden="true">·</span>
          <span id="liveRttText">Streamable HTTP (JSON + SSE)</span>
        </div>

        <div class="hero-tools-right">
          {{if .SourceURL}}
          <a href="{{.SourceURL}}" target="_blank" rel="noopener noreferrer" class="util-btn">源码仓库 ↗</a>
          {{end}}
          <button type="button" id="themeToggleBtn" class="util-btn" aria-label="切换深浅色主题">
            <span id="themeToggleLabel">切换浅色</span>
          </button>
        </div>
      </div>

      <div class="hero-grid">
        <div class="hero-copy">
          <div>
            <h1 class="hero-title">跨云端与个人终端的自托管 MCP 控制平面</h1>
            <p class="hero-lead">
              将 Linux 云主机、macOS 与 Windows 本地工作区收敛至单一加密 Streamable HTTP 枢纽。内置 OAuth 2.1 (PKCE S256) 授权服务、反向 WebSocket 设备配对与目录级沙箱护栏，让网页端 AI 与本地 IDE 安全协同。
            </p>
          </div>

          <div class="hero-actions">
            <div class="endpoint-bar">
              <div class="endpoint-bar-left">
                <span class="endpoint-tag mono">MCP</span>
                <span id="mcpEndpointVal" class="endpoint-url mono">{{.MCPURL}}</span>
              </div>
              <button type="button" id="copyEndpointBtn" class="copy-btn">复制端点</button>
            </div>

            <div class="cta-row">
              <a href="/agents" class="btn-primary">管理接入设备 (/agents)</a>
              <a href="#quickstart" class="btn-secondary">查看客户端接入配置</a>
            </div>
          </div>
        </div>

        <!-- Live Multi-Node Architecture Schematic & Prober -->
        <div class="blueprint-panel">
          <div class="bp-head">
            <span class="bp-title">实时多端数据流拓扑</span>
            <button type="button" id="runLiveProbeBtn" class="bp-probe-btn">在线探测 /healthz</button>
          </div>

          <div class="bp-diagram">
            <div class="topo-stage">
              <div>
                <div class="stage-name">上游 AI 客户端</div>
                <div class="stage-sub">Claude · ChatGPT · Gemini · Cursor · CLI</div>
              </div>
              <div class="stage-right mono">{{if .AuthOn}}OAuth 2.1 + PKCE{{else}}直连模式{{end}}</div>
            </div>

            <div class="topo-connector mono" aria-hidden="true">
              <span>│</span>
              <span>Streamable HTTP · POST / GET /mcp</span>
              <span>▼</span>
            </div>

            <div class="topo-stage is-hub">
              <div>
                <div class="stage-name">omni-mcp 控制枢纽</div>
                <div class="stage-sub mono">{{.BaseURL}}</div>
              </div>
              <div class="stage-right mono">策略护栏 + 审计</div>
            </div>

            <div class="topo-connector mono" aria-hidden="true">
              <span>│</span>
              <span>反向 WSS 多路复用 · 免公网端口</span>
              <span>▼</span>
            </div>

            <div class="topo-stage">
              <div>
                <div class="stage-name">跨平台受控节点机群</div>
                <div class="stage-sub">VPS (local) · Linux SSH · macOS · Windows</div>
              </div>
              <div class="stage-right mono">目录级沙箱锁定</div>
            </div>
          </div>

          <div class="bp-readout mono" id="probeOutputBar">
            <span class="bp-readout-ok" id="probeSummaryText">GET /healthz · 就绪 ({{.ToolCount}} 项已挂载原语)</span>
            <span id="probeDetailText">协议 2025-03-26</span>
          </div>
        </div>
      </div>

      <!-- 4-Cell Blueprint Telemetry Strip -->
      <div class="kpi-strip">
        <div class="kpi-cell">
          <div class="kpi-label">协议传输规范</div>
          <div class="kpi-val mono">Streamable HTTP</div>
          <div class="kpi-sub">兼容 JSON-RPC 与 SSE 事件流</div>
        </div>
        <div class="kpi-cell">
          <div class="kpi-label">客户端鉴权机制</div>
          <div class="kpi-val">{{if .AuthOn}}OAuth 2.1 · S256{{else}}内网免鉴权模式{{end}}</div>
          <div class="kpi-sub">{{if .AuthOn}}支持 DCR 动态注册与静态密钥{{else}}建议公网环境开启 auth.enabled{{end}}</div>
        </div>
        <div class="kpi-cell">
          <div class="kpi-label">已挂载 MCP 工具</div>
          <div class="kpi-val mono">{{.ToolCount}} 项跨端原语</div>
          <div class="kpi-sub">支持 target 参数路由任意在线节点</div>
        </div>
        <div class="kpi-cell">
          <div class="kpi-label">设备配对中心</div>
          <div class="kpi-val mono">/agents 指挥台</div>
          <div class="kpi-sub mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="{{.BaseURL}}/agents">{{.BaseURL}}/agents</div>
        </div>
      </div>
    </section>

    <!-- Asymmetric Bento Capabilities (#guardrails) -->
    <section class="section" id="guardrails">
      <div class="sec-head">
        <div>
          <h2 class="sec-title">纵深安全护栏与跨网调度架构</h2>
          <p class="sec-desc">拒绝裸奔暴露宿主机权限：从文件系统边界、破坏性命令二次确认到单节点独立密钥，层层收敛执行半径。</p>
        </div>
      </div>

      <div class="bento-grid">
        <article class="bento-card span-7">
          <div>
            <div class="bento-num mono">01. 目录级路径沙箱锁定</div>
            <h3 class="bento-title">严密约束读写边界，底层阻断目录穿越与敏感凭据访问</h3>
            <p class="bento-body">
              每台 Agent 节点显式声明允许访问的根目录白名单（<code>--allow-dir</code>）。所有文件读取、模糊检索与补丁写入均经过真实路径归一化（<code>EvalSymlinks</code>）校验，越界请求直接拒绝。
            </p>
          </div>
          <div class="bento-spec mono">
            <div class="spec-row">
              <span>read / apply_patch ──▶ ~/projects/omni-mcp/internal/*</span>
              <span class="spec-allow">允许读写</span>
            </div>
            <div class="spec-row">
              <span>read ──▶ ~/.ssh/id_ed25519 · /etc/shadow · ../../</span>
              <span class="spec-deny">沙箱拦截</span>
            </div>
          </div>
        </article>

        <article class="bento-card span-5">
          <div>
            <div class="bento-num mono">02. 高危指令二次确认护栏</div>
            <h3 class="bento-title">终端命令默认关闭，危险操作强制挂起确认</h3>
            <p class="bento-body">
              桌面与命令行节点默认仅开放文件读写；显式启用 <code>--allow-exec</code> 后，遇到递归删除、系统关机或权限变更等高危模式仍会触发确认令牌拦截。
            </p>
          </div>
          <div class="bento-spec mono">
            <div class="spec-row">
              <span>go test ./... · git status</span>
              <span class="spec-allow">直接放行</span>
            </div>
            <div class="spec-row">
              <span>rm -rf / · reboot · mkfs</span>
              <span class="spec-deny">挂起待确认</span>
            </div>
          </div>
        </article>

        <article class="bento-card span-5">
          <div>
            <div class="bento-num mono">03. 零公网端口八位码配对</div>
            <h3 class="bento-title">家庭宽带与移动办公电脑秒级接入自托管 Hub</h3>
            <p class="bento-body">
              个人电脑无需配置内网穿透或 SSH 免密。启动本地客户端即可生成一次性八位配对码，在浏览器登录 <code>/agents</code> 点击批准后自动签发独立令牌并存入系统钥匙串。
            </p>
          </div>
          <div class="bento-spec mono">
            <div class="spec-row">
              <span>1. 本机发起 /agent/pair/start</span>
              <span class="spec-allow">生成 XXXX-XXXX</span>
            </div>
            <div class="spec-row">
              <span>2. 管理员批准 ──▶ 写入 Keychain / DPAPI</span>
              <span class="spec-allow">WSS 长连接就绪</span>
            </div>
          </div>
        </article>

        <article class="bento-card span-7">
          <div>
            <div class="bento-num mono">04. 原生 OAuth 2.1 与动态客户端注册</div>
            <h3 class="bento-title">开箱兼容网页端大模型连接器与桌面开发工具</h3>
            <p class="bento-body">
              内置完整的 <code>/.well-known/oauth-authorization-server</code> 发现端点、RFC 7591 动态客户端注册（DCR）与 PKCE (S256) 换票流程，同时支持静态 Bearer 密钥供自动化代理长效直连。
            </p>
          </div>
          <div class="bento-spec mono">
            <div class="spec-row">
              <span>GET /.well-known/oauth-authorization-server</span>
              <span class="spec-allow">自动发现端点</span>
            </div>
            <div class="spec-row">
              <span>Authorization: Bearer &lt;static_or_oauth_token&gt;</span>
              <span class="spec-allow">双轨鉴权就绪</span>
            </div>
          </div>
        </article>
      </div>
    </section>

    <!-- Interactive Integration Workbench (#quickstart) -->
    <section class="section" id="quickstart">
      <div class="sec-head">
        <div>
          <h2 class="sec-title">多端接入配置工作台</h2>
          <p class="sec-desc">所有示例已自动填入当前枢纽地址 <span class="mono">{{.BaseURL}}</span>，切换左侧场景即可一键复制。</p>
        </div>
      </div>

      <div class="workbench">
        <div class="wb-rail" role="tablist" aria-label="接入场景选择">
          <button type="button" class="wb-tab active" role="tab" data-snippet="webai">
            <span class="wb-tab-title">01. 网页端 AI 连接器</span>
            <span class="wb-tab-sub">Claude · ChatGPT · Gemini 网页版</span>
          </button>
          <button type="button" class="wb-tab" role="tab" data-snippet="ide">
            <span class="wb-tab-title">02. 本地 IDE (mcp.json)</span>
            <span class="wb-tab-sub">Cursor · Windsurf · Claude Desktop</span>
          </button>
          <button type="button" class="wb-tab" role="tab" data-snippet="agent">
            <span class="wb-tab-title">03. 接入个人电脑节点</span>
            <span class="wb-tab-sub">Windows · macOS · Linux Agent</span>
          </button>
          <button type="button" class="wb-tab" role="tab" data-snippet="curl">
            <span class="wb-tab-title">04. 静态密钥命令行调用</span>
            <span class="wb-tab-sub">Streamable HTTP JSON-RPC 调试</span>
          </button>
          <button type="button" class="wb-tab" role="tab" data-snippet="probe">
            <span class="wb-tab-title">05. 健康检查与 OAuth 发现</span>
            <span class="wb-tab-sub">/healthz 与 .well-known 自检</span>
          </button>
        </div>

        <div class="wb-stage">
          <div class="wb-stage-head">
            <span id="wbTitleLabel" class="mono">网页端自定义 MCP 连接器配置</span>
            <button type="button" id="wbCopyBtn" class="wb-copy-code">复制代码</button>
          </div>
          <pre id="wbCodeBlock" class="wb-pre mono"></pre>
          <div id="wbFootNote" class="wb-foot">支持 OAuth 2.1 动态客户端注册，粘贴地址后按页面引导登录一次即可自动续期。</div>
        </div>
      </div>
    </section>

    <!-- Registered Tool Specification Matrix (#tools) -->
    <section class="section" id="tools">
      <div class="sec-head">
        <div>
          <h2 class="sec-title">已注册跨端工具规范目录</h2>
          <p class="sec-desc">当前枢纽已挂载 <span class="mono">{{.ToolCount}}</span> 项工具，所有文件与终端原语均支持通过 <code class="mono">target</code> 指定目标机器。</p>
        </div>
      </div>

      <div class="tool-panel">
        <div class="tool-toolbar">
          <div class="seg-bar" role="tablist" aria-label="工具分类筛选">
            <button type="button" class="seg-btn active" data-cat="all">全部 ({{.ToolCount}})</button>
            <button type="button" class="seg-btn" data-cat="fleet">节点机群</button>
            <button type="button" class="seg-btn" data-cat="fs">文件系统</button>
            <button type="button" class="seg-btn" data-cat="exec">终端会话</button>
          </div>
          <input type="search" id="toolSearchInput" class="tool-search" placeholder="搜索工具名称或说明…" aria-label="搜索工具">
        </div>

        <div id="toolGridContainer" class="tool-grid"></div>
        <div id="toolEmptyState" class="tool-empty" style="display:none;">未找到匹配当前筛选条件的工具。</div>
      </div>
    </section>
  </main>

  <footer class="footer">
    <div class="container footer-inner">
      <div>
        <strong style="color:var(--fg)">omni-mcp</strong>
        <span> · 自托管跨端 MCP 控制枢纽 · </span>
        <span class="mono">{{.Version}}</span>
      </div>
      <div class="footer-links mono">
        <a href="/healthz">/healthz</a>
        <a href="/.well-known/oauth-authorization-server">/.well-known/oauth</a>
        <a href="/agents">/agents 设备管理</a>
        {{if .SourceURL}}<a href="{{.SourceURL}}" target="_blank" rel="noopener noreferrer">GitHub</a>{{end}}
      </div>
    </div>
  </footer>

  <script>
    (function () {
      var BASE_URL = {{.BaseURL}};
      var MCP_URL = {{.MCPURL}};
      var TOOLS = {{.Tools}} || [];

      if (!BASE_URL) {
        BASE_URL = window.location.origin;
        MCP_URL = BASE_URL + "/mcp";
        var epEl = document.getElementById("mcpEndpointVal");
        if (epEl) epEl.textContent = MCP_URL;
      }

      // Theme Switcher
      var themeBtn = document.getElementById("themeToggleBtn");
      var themeLabel = document.getElementById("themeToggleLabel");
      function syncThemeUI() {
        var isDark = document.documentElement.getAttribute("data-theme") === "dark";
        if (themeLabel) themeLabel.textContent = isDark ? "切换浅色" : "切换深色";
      }
      if (themeBtn) {
        themeBtn.onclick = function () {
          var next = document.documentElement.getAttribute("data-theme") === "dark" ? "light" : "dark";
          document.documentElement.setAttribute("data-theme", next);
          try {
            localStorage.setItem("omni-theme", next);
            localStorage.setItem("omni-steep-theme", next);
          } catch (e) {}
          syncThemeUI();
        };
      }
      syncThemeUI();

      // Copy Endpoint Button
      var copyEpBtn = document.getElementById("copyEndpointBtn");
      if (copyEpBtn) {
        copyEpBtn.onclick = function () {
          navigator.clipboard.writeText(MCP_URL).then(function () {
            copyEpBtn.textContent = "已复制";
            setTimeout(function () { copyEpBtn.textContent = "复制端点"; }, 1600);
          }).catch(function () {});
        };
      }

      // Live /healthz Probe
      function runHealthProbe(manual) {
        var btn = document.getElementById("runLiveProbeBtn");
        var sumEl = document.getElementById("probeSummaryText");
        var detEl = document.getElementById("probeDetailText");
        var rttTop = document.getElementById("liveRttText");
        var badgeTop = document.getElementById("liveHealthBadge");
        if (manual && btn) btn.textContent = "探测中…";
        var t0 = performance.now();
        fetch("/healthz", { cache: "no-store" })
          .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, status: r.status, data: d }; }); })
          .then(function (res) {
            var rtt = Math.max(1, Math.round(performance.now() - t0));
            if (res.ok) {
              if (sumEl) sumEl.textContent = "HTTP " + res.status + " · status=" + (res.data.status || "ok") + " · 工具 " + ((res.data.tools || []).length) + " 项";
              if (detEl) detEl.textContent = "实测延迟 " + rtt + " ms";
              if (rttTop) rttTop.textContent = "实测 RTT " + rtt + " ms · Streamable HTTP";
              if (badgeTop) badgeTop.textContent = "Hub 在线就绪";
            } else {
              if (sumEl) sumEl.textContent = "HTTP " + res.status + " · 响应异常";
            }
          })
          .catch(function () {
            if (sumEl) sumEl.textContent = "本地离线预览模式 · 端点模板已就绪";
          })
          .finally(function () {
            if (manual && btn) {
              btn.textContent = "探测完成";
              setTimeout(function () { btn.textContent = "在线探测 /healthz"; }, 1400);
            }
          });
      }
      var probeBtn = document.getElementById("runLiveProbeBtn");
      if (probeBtn) probeBtn.onclick = function () { runHealthProbe(true); };
      runHealthProbe(false);

      // Workbench Snippets
      var SNIPPETS = {
        webai: {
          title: "网页端 AI 连接器接入步骤 (OAuth 2.1 + PKCE)",
          foot: "无需手动填写 Client ID，omni-mcp 会通过动态客户端注册自动协商凭证。",
          code: [
            "# 1. 在 Claude / ChatGPT / Gemini 的 MCP 连接器中添加自定义服务：",
            "MCP Server URL: " + MCP_URL,
            "",
            "# 2. 点击连接后将自动跳转至本枢纽 OAuth 2.1 授权页：",
            "Authorization : " + BASE_URL + "/oauth/authorize",
            "Token Endpoint: " + BASE_URL + "/oauth/token",
            "",
            "# 3. 使用 Hub 管理员账号核验一次后，即可在对话中直接指定节点：",
            "\"请先调用 servers 查看我在线的设备，然后读取 mygw 上的项目结构\""
          ].join("\n")
        },
        ide: {
          title: "mcp.json · Cursor / Windsurf / Claude Desktop",
          foot: "若客户端使用静态令牌模式，可通过 Authorization 请求头传入 Bearer 密钥。",
          code: JSON.stringify({
            mcpServers: {
              "omni-mcp": {
                url: MCP_URL,
                headers: {
                  Authorization: "Bearer <YOUR_STATIC_OR_OAUTH_TOKEN>"
                }
              }
            }
          }, null, 2)
        },
        agent: {
          title: "在个人电脑或内网主机挂载受控 Agent 节点",
          foot: "不带 --allow-exec 时默认禁止运行终端命令，仅开放指定目录读写。",
          code: [
            "# 方式 A：双击启动桌面图形控制台（推荐 Windows / macOS）",
            "omni-mcp agent-ui",
            "",
            "# 方式 B：命令行一键发起八位码反向配对（无需公网 IP 或 SSH）",
            "omni-mcp agent \\",
            "  --hub " + BASE_URL + " \\",
            "  --name my-workstation \\",
            "  --allow-dir ~/projects",
            "",
            "# 随后浏览器打开 " + BASE_URL + "/agents 核对配对码并点击「批准接入」"
          ].join("\n")
        },
        curl: {
          title: "Streamable HTTP JSON-RPC 命令行直连调试",
          foot: "同时兼容 application/json 与 text/event-stream 流式返回格式。",
          code: [
            "curl -i -X POST " + MCP_URL + " \\",
            "  -H \"Content-Type: application/json\" \\",
            "  -H \"Accept: application/json, text/event-stream\" \\",
            "  -H \"Authorization: Bearer $MCP_STATIC_TOKEN\" \\",
            "  -d \x27{",
            "    \"jsonrpc\": \"2.0\",",
            "    \"id\": 1,",
            "    \"method\": \"initialize\",",
            "    \"params\": {",
            "      \"protocolVersion\": \"2025-03-26\",",
            "      \"capabilities\": {},",
            "      \"clientInfo\": { \"name\": \"cli-probe\", \"version\": \"1.0.0\" }",
            "    }",
            "  }\x27"
          ].join("\n")
        },
        probe: {
          title: "服务端健康状态与 OAuth 2.1 元数据自检",
          foot: "可用于反向代理连通性监控与自动化部署验收。",
          code: [
            "# 探测 Hub 运行状态与已注册工具清单",
            "curl -s " + BASE_URL + "/healthz | jq .",
            "",
            "# 检查 RFC 8414 OAuth 2.1 授权服务发现元数据",
            "curl -s " + BASE_URL + "/.well-known/oauth-authorization-server | jq ."
          ].join("\n")
        }
      };

      var wbTitle = document.getElementById("wbTitleLabel");
      var wbCode = document.getElementById("wbCodeBlock");
      var wbFoot = document.getElementById("wbFootNote");
      var wbTabs = document.querySelectorAll(".wb-tab");
      var activeKey = "webai";

      function renderSnippet(key) {
        activeKey = key;
        var item = SNIPPETS[key] || SNIPPETS.webai;
        if (wbTitle) wbTitle.textContent = item.title;
        if (wbCode) wbCode.textContent = item.code;
        if (wbFoot) wbFoot.textContent = item.foot;
        wbTabs.forEach(function (t) {
          t.classList.toggle("active", t.getAttribute("data-snippet") === key);
        });
      }
      wbTabs.forEach(function (t) {
        t.onclick = function () { renderSnippet(t.getAttribute("data-snippet")); };
      });
      renderSnippet("webai");

      var wbCopy = document.getElementById("wbCopyBtn");
      if (wbCopy) {
        wbCopy.onclick = function () {
          var text = (SNIPPETS[activeKey] || SNIPPETS.webai).code;
          navigator.clipboard.writeText(text).then(function () {
            wbCopy.textContent = "已复制";
            setTimeout(function () { wbCopy.textContent = "复制代码"; }, 1600);
          }).catch(function () {});
        };
      }

      // Tool Specification Matrix
      var TOOL_SPECS = {
        servers: {
          cat: "fleet",
          catLabel: "节点机群",
          desc: "枚举当前所有在线与已配置的机器节点（本地主机、SSH 远端及反向接入的 Windows/macOS Agent）及负载、内存与工作区状态。",
          sig: "参数: { refresh?: boolean }"
        },
        read: {
          cat: "fs",
          catLabel: "文件系统",
          desc: "按行号偏移量分页读取指定节点沙箱内的文本文件，或安全检视图像元数据，内置大小上限保护。",
          sig: "参数: { target?: string, path: string, offset?: number, limit?: number }"
        },
        glob: {
          cat: "fs",
          catLabel: "文件系统",
          desc: "使用通配模式快速枚举目标工作区内的目录树与文件元数据，自动跳过大体积构建产物目录。",
          sig: "参数: { target?: string, path?: string, pattern: string }"
        },
        search: {
          cat: "fs",
          catLabel: "文件系统",
          desc: "跨多文件执行高性能正则表达式与关键字内容检索，返回精确文件路径、行号与上下文片段。",
          sig: "参数: { target?: string, path?: string, pattern: string }"
        },
        apply_patch: {
          cat: "fs",
          catLabel: "文件系统",
          desc: "在目标节点的授权沙箱目录内执行原子级差异补丁修改、新建文件或精确代码块替换。",
          sig: "参数: { target?: string, patch: string }"
        },
        shell: {
          cat: "exec",
          catLabel: "终端会话",
          desc: "在已显式开启命令执行权限的目标节点上运行终端指令（Bash / PowerShell），受高危命令确认策略约束。",
          sig: "参数: { target?: string, command: string, cwd?: string, timeout_ms?: number }"
        },
        shell_output: {
          cat: "exec",
          catLabel: "终端会话",
          desc: "增量拉取后台长耗时构建、测试或服务进程的实时标准输出与退出状态码。",
          sig: "参数: { target?: string, session_id: string }"
        },
        shell_kill: {
          cat: "exec",
          catLabel: "终端会话",
          desc: "向指定的后台终端会话发送终止信号并回收子进程资源。",
          sig: "参数: { target?: string, session_id: string }"
        }
      };

      var currentCat = "all";
      var searchKw = "";
      var gridEl = document.getElementById("toolGridContainer");
      var emptyEl = document.getElementById("toolEmptyState");

      function esc(s) {
        return String(s).replace(/[&<>"']/g, function (c) {
          return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
        });
      }

      function renderTools() {
        if (!gridEl) return;
        var list = TOOLS.map(function (name, idx) {
          var meta = TOOL_SPECS[name] || {
            cat: "fleet",
            catLabel: "扩展工具",
            desc: "通过上游 MCP 聚合或自定义注册挂载的扩展工具原语。",
            sig: "通过 tools/list 查看完整 JSON Schema"
          };
          return { idx: idx + 1, name: name, cat: meta.cat, catLabel: meta.catLabel, desc: meta.desc, sig: meta.sig };
        }).filter(function (item) {
          if (currentCat !== "all" && item.cat !== currentCat) return false;
          if (searchKw) {
            var hay = (item.name + " " + item.desc + " " + item.catLabel).toLowerCase();
            if (hay.indexOf(searchKw) === -1) return false;
          }
          return true;
        });

        if (!list.length) {
          gridEl.style.display = "none";
          if (emptyEl) emptyEl.style.display = "block";
          return;
        }
        gridEl.style.display = "grid";
        if (emptyEl) emptyEl.style.display = "none";

        gridEl.innerHTML = list.map(function (item) {
          var num = (item.idx < 10 ? "0" : "") + item.idx;
          return '<div class="tool-cell">' +
            '<div class="tool-top">' +
              '<span class="tool-name mono">' + num + '. ' + esc(item.name) + '</span>' +
              '<span class="tool-cat">' + esc(item.catLabel) + '</span>' +
            '</div>' +
            '<p class="tool-desc">' + esc(item.desc) + '</p>' +
            '<div class="tool-sig mono">' + esc(item.sig) + '</div>' +
          '</div>';
        }).join("");
      }

      document.querySelectorAll("[data-cat]").forEach(function (btn) {
        btn.onclick = function () {
          document.querySelectorAll("[data-cat]").forEach(function (b) { b.classList.remove("active"); });
          btn.classList.add("active");
          currentCat = btn.getAttribute("data-cat");
          renderTools();
        };
      });
      var searchInput = document.getElementById("toolSearchInput");
      if (searchInput) {
        searchInput.oninput = function (e) {
          searchKw = (e.target.value || "").trim().toLowerCase();
          renderTools();
        };
      }
      renderTools();
    })();
  </script>
</body>
</html>`))

func renderLanding(w http.ResponseWriter, cfg *config.Config, tools []string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := landingData{
		BaseURL:   cfg.Server.BaseURL,
		MCPURL:    cfg.Server.BaseURL + "/mcp",
		SourceURL: cfg.Server.SourceURL,
		Tools:     tools,
		AuthOn:    cfg.Auth.Enabled,
		Version:   Version,
		ToolCount: len(tools),
	}
	_ = landingTmpl.Execute(w, data)
}
