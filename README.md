# omni-mcp

`omni-mcp` is a single-project MCP Hub for Linux VPSs and Windows, Linux, and macOS workstations. One Go module builds the public HTTP MCP server and native Agents. Agents initiate an outbound authenticated `wss://` connection to the Hub, so a workstation needs no public IP, inbound port, separate MCP server, or device manifests.

```text
Gemini / Claude → HTTPS /mcp → Omni MCP Hub → authenticated WebSocket
                                              → Windows / Linux / macOS Agents → registered folders
```

## Build and test

Requires Go 1.25 or later.

```sh
go test ./...
make build                 # Hub for the current operating system
make build-windows         # bin/omni-mcp-windows-amd64.exe
make build-linux-amd64
make build-linux-arm64
make build-all
```

The integration tests cover per-device Hub/Agent pairing, duplicate-name rejection, reconnect behavior, target discovery, confined file operations, search, glob, process cleanup, and platform-specific shells. GitHub Release builds publish matching Hub and Agent binaries together.

## Hub

The Hub provides the HTTPS `/mcp` Streamable HTTP endpoint and `GET /agent/ws` WebSocket endpoint. Run it on Linux behind an HTTPS reverse proxy. Copy `configs/config.example.yaml`, set the public base URL and MCP OAuth user, and protect each device's pairing token in a `0600`-permission environment file:

```sh
./bin/omni-mcp gen-token
sudo install -d -m 0750 /etc/omni-mcp
sudo install -m 0600 /dev/null /etc/omni-mcp/agent.env
sudoedit /etc/omni-mcp/agent.env
```

```text
OMNI_AGENT_TOKEN_OFFICE_PC=<generated-office-pc-token>
OMNI_AGENT_TOKEN_LAB_PC=<generated-lab-pc-token>
```

Use `omni-mcp gen-token` to create one high-entropy token per Agent. The variable suffix is the Agent name in uppercase with hyphens replaced by underscores, so `office-pc` uses `OMNI_AGENT_TOKEN_OFFICE_PC`. Remove only that device's variable and restart the Hub to revoke its token.

Configure systemd or Docker Compose to load that environment, then serve `/agent/ws` as a WebSocket upgrade to the same loopback Hub port used for `/mcp`. Merge `deploy/nginx-agent-ws.conf.example` into your HTTPS Nginx virtual host when using Nginx. The new service unit is `deploy/omni-mcp.service`; it does not alter an existing `runabout.service`.

## Agents

Download the binary for the target OS from the same release as the Hub. In a standard, non-administrator Windows PowerShell session:

```powershell
.\omni-mcp-windows-amd64.exe agent `
  --hub https://mcp.example.com `
  --name office-pc `
  --allow-dir 'D:\Projects\App'
```

For an interactive launch, omit the environment assignment: the Agent securely prompts for the token without echoing it. The name-derived environment variable is useful for a managed background service.

The Agent authenticates with TLS certificate verification, retries interrupted connections, and registers its unique name and allowed project folders immediately after pairing. Repeat `--allow-dir` for additional folders, or run a separate Agent with a different name on another workstation. Each Agent name needs its own Hub token; a duplicate online name is rejected without interrupting the active connection. No JSON device manifest or Hub YAML edits are needed when machines join or leave.

After `已连接 Omni MCP Hub` appears, reconnect your Gemini/Claude MCP client, call `servers`, then use the workstation as an ordinary `target`:

```text
glob(target="office-pc", pattern="**/*.sln")
read(target="office-pc", path="D:\\Projects\\App\\README.md")
search(target="office-pc", path="D:\\Projects\\App", pattern="TODO")
apply_patch(target="office-pc", patch="...")
```

The Agent can run automatically at user sign-in through Task Scheduler on Windows or a user service on Linux/macOS. It disappears from the target list while offline and automatically re-registers on reconnect.

## Command execution

Shell command execution is disabled by default. Add `--allow-exec` only when you accept granting the Agent the full permissions of the local account that started it. Windows prefers `pwsh.exe` when installed and falls back to Windows PowerShell 5.1; commands must use PowerShell 5.1-compatible syntax, including semicolons rather than `&&` or `||`. Linux and macOS Agents use Bash when installed and otherwise use `sh`:

```powershell
.\omni-mcp-windows-amd64.exe agent `
  --hub https://mcp.example.com `
  --name office-pc `
  --allow-dir 'D:\Projects\App' `
  --allow-exec
```

Agent file access uses Go `os.Root` and rejects traversal and symbolic-link/reparse-point escapes from every registered folder. The folder boundary **does not sandbox shell commands or child processes**. Run the Agent as a standard user and enable command execution only for trusted MCP clients.

## Configuration and deployment

- Hub OAuth and server configuration: `configs/config.example.yaml`.
- Dynamic Agent discovery: per-device tokens named `OMNI_AGENT_TOKEN_<NAME>`; `agent --name` and repeated `--allow-dir` arguments.
- Existing local Linux, SSH fleet and MCP upstreams continue to run through the same Hub.
- Docker Compose: `deploy/docker-compose.yml`.
- Bare metal systemd: `deploy/omni-mcp.service`.
- WebSocket reverse proxy example: `deploy/nginx-agent-ws.conf.example`.

Run the commands from `README.zh.md` for the complete first-time setup sequence.

## CLI

```text
omni-mcp serve [-c config.yaml]
omni-mcp agent --hub URL --name NAME --allow-dir PATH [--allow-exec]
omni-mcp gen-token
omni-mcp check-policy 'command'
omni-mcp version
```

## License

Licensed under GNU AGPL-3.0-or-later. This combined project incorporates and modifies `runabout`, Copyright (C) 2026 noir017. When making a modified network service available to users, provide them access to the corresponding source as required by AGPL section 13. Set `server.source_url` to this release's source repository.
