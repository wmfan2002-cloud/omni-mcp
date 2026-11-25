# Omni MCP Architecture

## Goal

Build one independently versioned Go project that serves MCP clients on a Linux Hub and runs authenticated Windows, Linux, and macOS Agents from the same source tree and release. Keep the existing Go MCP/OAuth/tool/policy implementation as the Hub core. Integrate workstation access behind the existing fleet abstraction instead of fronting a second MCP server.

## Runtime topology

```text
MCP client
  └─ HTTPS Streamable HTTP /mcp
       └─ Linux Omni MCP Hub
            ├─ existing local tools and optional SSH targets
            ├─ dynamic in-memory Agent target registry
            └─ authenticated TLS WebSocket /agent/ws
                 ├─ office-pc Agent session
                 └─ build-linux Agent session
```

The Hub keeps no outbound connection list in YAML. Each Agent name has an independent `OMNI_AGENT_TOKEN_<NAME>` bearer token, checked before WebSocket upgrade. The authenticated Agent sends `hello` with its validated name, OS, shell, binary version, active allowed roots, and command-execution opt-in state; the Hub updates the existing target registry immediately. Disconnect removes the current session's target. A duplicate online name is rejected without replacing the live session.

Every operation uses a request ID and the WebSocket's one-to-one transport pairing. The Hub sends at most one authenticated response to each pending RPC, applies the MCP operation's context deadline, and discards the pending ID after completion. Binary file contents are base64 encoded under a bounded 48 MiB WebSocket payload limit. Both client and server reconnect/replace their sessions safely. Heartbeats are sent as data messages on a 25-second interval.

## Tools and routing

The existing MCP tools continue to use `target`, `Fleet.Run`, and `Fleet.FS`. The Agent transport adds one `AgentFS` adapter implementing the current file API, plus native `fs.search`, `fs.glob`, and `system.info` Agent operations. MCP callers therefore use the same `read`, `apply_patch`, `glob`, `search`, and `servers` tools for local Linux, SSH, Windows, Linux, and macOS.

Command tools use `Fleet.Run` and `Fleet.Start` with Agent RPCs. Windows Agents prefer `pwsh.exe` and fall back to `powershell.exe`; tool descriptions prescribe PowerShell 5.1-compatible syntax. Linux/macOS Agents prefer Bash and fall back to `sh`. Command execution is disabled unless the Agent started with `--allow-exec`. Registered roots constrain native Hub file tools but do not constrain shell commands, scripts, child processes, filesystem ACLs, or network access. Shell commands are not claimed to be sandboxed; their effective boundary is the local account running the Agent.

## Workspace boundaries

Each Agent `--allow-dir` is opened once using Go `os.OpenRoot`. Agent file operations resolve every relative/absolute user path to an opened allowed root and then use that root's `Stat`, `Lstat`, `ReadFile`, `WriteFile`, `Remove`, `MkdirAll`, and `Rename` methods. The standard library rejects symbolic-link/reparse-point traversals out of that root. Multi-file patches are planned by the existing Hub before commit; individual writes use a same-directory temporary file and rooted rename.

The Agent also enforces output/file size bounds, UTF-8 PowerShell setup, clean child-process environment inheritance, optional command disablement, per-agent names, and hidden token entry. Completed background processes are removed after a final poll or a bounded retention period; session disconnect cancels RPCs, kills Agent-owned background process trees, and clears their records. Agent registration is ephemeral runtime state: no directory manifests, edit/restart workflow, or credentials in the source tree.

## Trust boundary

`OMNI_AGENT_TOKEN_<NAME>` is a high-entropy per-device bearer token that authorizes enrollment under one Agent name. Store tokens in the Hub service environment, not config YAML or a WebSocket URL. Removing one named token and restarting the Hub revokes that device without changing other Agent credentials. Existing MCP `allowed_targets` rules restrict which credentials may address dynamic Agent names. Each Agent then exposes its declared workspace folders to authenticated Hub users permitted for that target.

Agent shell execution carries the local account's full privileges and must only be enabled for trusted MCP users. Command filtering for POSIX shell on the Hub is not a PowerShell security policy. A future OS process sandbox must enforce whole-process-tree filesystem/network access before this command mode can be presented as workspace-contained.

## Platforms and builds

The Hub is supported on Linux amd64/arm64. Releases publish Windows amd64 and Linux Agent binaries; macOS Agents can be built from source. A small `internal/process` package isolates POSIX process-group signals from Windows hidden-process startup and process-tree cleanup; this keeps Windows cross-compilation of the Hub package feasible and supplies the shared Agent command executor.

CI runs Go unit/integration tests, vet, native Linux builds, and Linux→Windows x64 cross-builds. Tagged GitHub Releases publish `omni-mcp-linux-amd64.tar.gz`, `omni-mcp-linux-arm64.tar.gz`, `omni-mcp-windows-amd64.exe`, and `SHA256SUMS` under the same version tag.
