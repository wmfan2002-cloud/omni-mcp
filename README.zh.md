# omni-mcp

`omni-mcp` 是一个独立发布的远程 MCP Hub：Hub 运行在你的 VPS 上；Windows、Linux、macOS 电脑运行同一项目构建出的 Agent，主动通过 HTTPS/WSS 连接 Hub。电脑无需公网 IP、SSH、Tailscale、Coding Tools MCP 或单独注册 upstream，Gemini、Claude 等 MCP 客户端只连接一个 Hub 地址。

```text
Gemini / Claude
  → HTTPS · /mcp
  → Omni MCP Hub（Linux VPS）
  → 对应 Agent 的安全 WebSocket 会话
  → 注册的 Workspace
  → 目标系统 shell / 文件系统
```

## 构建

需要 Go 1.25 或更新版本。代码、Hub 和 Windows Agent 共用一个 Go module、一个 GitHub Release 与同一套 CLI：

```sh
go test ./...
make build                 # 本机 Hub
make build-windows         # Windows x64 Agent: bin/omni-mcp-windows-amd64.exe
make build-linux-amd64
make build-linux-arm64
make build-darwin          # macOS arm64 Agent
make build-all             # Linux x64、Linux arm64 与 Windows x64
```

`go test` 包含 Hub/Agent WebSocket 配对、逐设备令牌、重复名称拒绝、断线清理、目标发现、文件读写、搜索、glob 和跨系统 shell 测试。Agent 构建为终端程序；首次运行会在本地终端安全读取 Hub 配对令牌。无需将令牌写在命令行历史或进程参数中。

## Hub 部署

首次试用建议使用 VPS 上的独立 `omni-mcp` service 和新的独立 MCP URL（例如 `https://omni-mcp.example.com/mcp`），与现有 `runabout.service` 隔离。裸机默认监听回环 `127.0.0.1:8485`，Docker 默认映射到宿主机 `127.0.0.1:8485`；这与旧项目的 `8484` 端口分开。Nginx/Caddy/Cloudflare Tunnel 负责 HTTPS，并将新域名的 `/mcp` 和 `/agent/ws` 路由到 `8485`。

为每台 Agent 生成独立的高熵配对令牌：

```sh
./bin/omni-mcp gen-token
sudo install -d -m 0750 /etc/omni-mcp
sudo install -m 0600 /dev/null /etc/omni-mcp/agent.env
sudoedit /etc/omni-mcp/agent.env
```

在 Hub 环境文件中按 Agent 名添加配对令牌。名称 `office-pc` 对应变量 `OMNI_AGENT_TOKEN_OFFICE_PC`，名称 `lab-pc` 对应 `OMNI_AGENT_TOKEN_LAB_PC`：

```text
OMNI_AGENT_TOKEN_OFFICE_PC=<office-pc 的令牌>
OMNI_AGENT_TOKEN_LAB_PC=<lab-pc 的令牌>
```

编辑 `configs/config.example.yaml` 的副本，填写自己的 HTTPS MCP URL、OAuth 用户和工作目录。使用 `deploy/omni-mcp.service` 时，把配置放在 `/etc/omni-mcp/config.yaml` 并确认其文件权限合适；再修改 systemd override：

```sh
sudo systemctl edit omni-mcp
```

```ini
[Service]
EnvironmentFile=/etc/omni-mcp/agent.env
```

然后构建并启动独立 Hub：

```sh
make build
sudo install -m 0755 bin/omni-mcp /usr/local/bin/omni-mcp
sudo systemctl daemon-reload
sudo systemctl enable --now omni-mcp
sudo systemctl --no-pager status omni-mcp
curl http://127.0.0.1:8485/healthz
```

如果现有反向代理在入口 Nginx 上监听 HTTPS，将 `deploy/nginx-agent-ws.conf.example` 中的 WebSocket `location` 合并到对应 HTTPS `server` 块，再通过 `nginx -t` 和 reload 生效。该示例只处理 Agent 的 `/agent/ws` 长连接，原有 MCP location 按你的域名设置处理。

## Agent 电脑接入

### 方式一：桌面端应用（Windows & macOS 推荐，双击即用）

- **Windows 桌面版**：从 GitHub Release 下载 `omni-mcp-desktop-windows-amd64.exe` 直接双击（无黑框闪烁，自动以独立桌面窗口启动 `omni-mcp Console`）。
- **macOS 桌面版**：从 GitHub Release 下载 `OmniMCP-Desktop-macos-arm64.zip`（Apple Silicon）或 `OmniMCP-Desktop-macos-amd64.zip`（Intel），解压后把 `Omni MCP.app` 拖入「应用程序」双击即可。
  > 开源软件未做 Apple 商业公证，macOS 首次打开若提示无法验证开发者，执行一次 `xattr -cr "/Applications/Omni MCP.app"` 后双击即可正常运行。

桌面控制台核心特性：
- **全新 `omni-mcp Console` 界面**：支持中英双语与深浅主题切换、系统原生目录选择弹窗（Windows FolderBrowser / macOS Finder）、一键 Hub 配对向导、开关 `--allow-exec` 与自动连接；
- **系统级加密存储**：Windows 使用 **DPAPI 当前用户加密**，macOS 使用 **Keychain 钥匙串**，网页接口永不回传明文令牌；
- **实时 SSE 操作审计**：实时展示 AI 在本机执行的每一次读写、搜索与终端命令，并支持一键断开（急停）与退出程序；
- **安全加固**：仅绑定 `127.0.0.1`，带回环 Host 校验、HttpOnly SameSite=Strict 会话 Cookie 与 CSRF 防护。

### 方式二：命令行启动（适合后台脚本 / 服务器）

为试用电脑从同一版本 GitHub Release 下载对应系统的 Agent。Linux x64 使用 `omni-mcp-linux-amd64`，Linux arm64 使用 `omni-mcp-linux-arm64`，macOS Apple 芯片可用 `make build-darwin` 构建，Windows 使用 `omni-mcp-windows-amd64.exe`。Windows 在标准 PowerShell 中启动：

```powershell
.\omni-mcp-windows-amd64.exe agent `
  --hub https://omni-mcp.example.com `
  --name office-pc `
  --allow-dir 'D:\Projects\App'
```

交互式启动时可以省略环境变量赋值；Agent 会在终端中安全提示输入令牌且不回显。`OMNI_AGENT_TOKEN_OFFICE_PC` 适合任务计划程序等后台启动方式。

Linux/macOS 也以 `agent` 子命令运行同一可执行文件：

```sh
./omni-mcp-linux-amd64 agent --hub https://omni-mcp.example.com --name build-linux --allow-dir /srv/projects
```

交互式启动会安全提示输入令牌；配置为后台服务时，可通过受限权限的服务环境注入 `OMNI_AGENT_TOKEN_BUILD_LINUX`。

Agent 按设备名使用独立令牌，校验 TLS 证书并自动重试断开的 HTTPS/WSS 连接。终端显示 `已连接 Omni MCP Hub` 后，在 Gemini 中重新连接 Hub MCP，调用 `servers` 确认 `office-pc` 在线。重复在线名称会被拒绝并提示更换名称，不会踢掉当前连接。

需要同时开放其他项目目录时，可重复 `--allow-dir`：

```powershell
.\omni-mcp-windows-amd64.exe agent `
  --hub https://omni-mcp.example.com `
  --name office-pc `
  --allow-dir 'D:\Projects\App' `
  --allow-dir 'D:\Projects\Docs'
```

每台电脑设置自己的唯一 `--name`，Hub 为每个名字配置一个 `OMNI_AGENT_TOKEN_<NAME>`，并只登记需要的项目目录。设备名和每次启动时登记的目录会显示在 Hub 的 `servers` 与工具描述中。没有连接到 Hub 时，设备从 target 列表移除；Agent 重新上线后自动重新登记，无需修改 YAML/JSON。Windows 可用任务计划程序配置开机登录启动；更新 Agent 后重新启动该任务。

### 先测试文件与读取

先在 Gemini 中调用：

1. `servers` 查看已连接机器、系统、工作目录和命令执行开关。
2. `glob(target="office-pc", pattern="**/*.sln")` 查找注册目录里的 solution。
3. `read(target="office-pc", path="D:\\Projects\\App\\README.md")` 读取文件。
4. `search(target="office-pc", path="D:\\Projects\\App", pattern="TODO")` 搜索项目。
5. 确认文件工具按预期工作后，再调用 `apply_patch` 测试编辑。

Agent 内部使用 Go `os.Root`，文件操作会拒绝越过每个注册目录的符号链接/reparse-point 路径。目录白名单适用于 Hub 的原生文件、搜索与 glob 工具。

### 开启命令执行

Agent 默认**不开放 shell 命令执行**。这是有意的：文件目录白名单不构成 shell 进程的 OS 沙箱；执行权限等同于启动 Agent 的本地用户权限。

仅在接受这一权限范围并用**标准（非管理员）用户**运行的电脑上，显式加 `--allow-exec` 启动，例如：

```powershell
.\omni-mcp-windows-amd64.exe agent `
  --hub https://omni-mcp.example.com `
  --name office-pc `
  --allow-dir 'D:\Projects\App' `
  --allow-exec
```

Windows Agent 优先探测并使用 `pwsh.exe`，未安装时回退到系统自带的 `powershell.exe`（Windows PowerShell 5.1）。工具提示模型使用 PowerShell 5.1 兼容语法；多条命令用分号连接，不用 `&&`、`||`。Linux/macOS Agent 优先用 Bash，未安装时回退到 `sh`。

启用后 MCP `shell`、后台命令工具会在目标电脑上执行对应 shell。命令能使用本地用户本身拥有的文件、网络和进程权限，不会被局限在 `--allow-dir` 中。Hub 的危险命令策略按 POSIX/Bash 语法分析，**不构成 PowerShell 安全边界**；只给信任的 MCP 用户开放此选项。

## 使用同一可执行文件

```sh
omni-mcp version
omni-mcp serve -c /etc/omni-mcp/config.yaml
omni-mcp gen-token
omni-mcp check-policy 'rm -rf /tmp/example'
```

普通 Windows 试用启动是 `omni-mcp.exe agent ...`。两种角色通过命令选择运行模式，不需要安装第二个程序或再部署 Windows MCP server。

## 主要配置

- MCP Hub 与 OAuth：`configs/config.example.yaml` 中的 `server`、`auth` 段。
- Unix shell 与用户策略：原有工具和 `policy` 段。
- 静态 Linux/SSH target：`fleet.hosts` 段。
- 动态 Agent：为每台设备配置独立环境变量 `OMNI_AGENT_TOKEN_<NAME>`；在线目标自动发现，不需要常驻机群清单。比如 `office-pc` 使用 `OMNI_AGENT_TOKEN_OFFICE_PC`。
- 容器部署：`deploy/docker-compose.yml`；systemd 部署：`deploy/omni-mcp.service`。

Hub 可同时保留本机 Linux、已配置 SSH 机器、多个 Windows/Linux/macOS Agent 和 Streamable HTTP/stdio MCP upstream；客户端仍只配置同一个 Hub connector。所有活跃 Hub 用户的 `allowed_targets` 均与在线 Agent 的名称匹配，可以对动态 Agent 使用同一白名单。

## 系统边界与安全

- Agent 只接受 TLS `https/wss` Hub 地址；不支持公网服务模式或反向入站连接。每个设备独立配对凭据走 WebSocket 的 Bearer Authorization header，不记录在 URL 或连接日志中。移除某台机器对应的 `OMNI_AGENT_TOKEN_<NAME>` 并重启 Hub 即可单独吊销。
- 每个 Agent 只能访问本次启动参数提供的 Workspace 文件夹。所有文件传输和访问目录都使用 `os.Root` 限制边界。
- shell 命令执行独立关闭。显式加 `--allow-exec` 才会运行，权限等于本地登录用户；请使用普通用户。
- Hub 令牌持有者可以以设备名登记 Agent。配置 `auth.*.allowed_targets` 可以只向特定 MCP 用户显示具名目标；部署机必须在对应名称下设置 MCP client policy。
- 文件/搜索/glob 采用 Hub 现有路径保护与审计。Hub 上的 `apply_patch` 对多个文件先计划再落盘。
- Linux Hub 使用 AGPL-3.0-or-later。公开托管修改后的网络服务时，应在 `server.source_url` 提供本版本的可获取源码。

## 排障

- `agent --help` 展示完整运行参数。
- `401 Unauthorized`：核对 Agent 名对应的 `OMNI_AGENT_TOKEN_<NAME>` 与本机令牌是否一致，并确认重启 Hub 后读取了新环境变量。
- Agent 反复重连：检查 Hub `/healthz`、Nginx WebSocket 升级 location、`curl -v https://omni-mcp.example.com/agent/ws` 和客户端 TLS 证书。`/agent/ws` 的 GET 未升级请求返回 `426` 是预期行为。
- `servers` 没显示电脑：确认 `servers` 工具调用发生在 Agent 已登录后建立的新会话；MCP 客户端可能缓存 tools/list。
- 文件工具返回路径越界：确认调用的完整 Windows 路径落在启动 Agent 的某个 `--allow-dir` 下面。
- 命令执行拒绝：对这台电脑使用可信账户运行，并显式重启 Agent 加 `--allow-exec`；此选项授予该用户完整的 shell 命令权限。
