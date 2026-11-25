package tools

import (
	"strings"
	"testing"

	"omni-mcp/internal/config"
	"omni-mcp/internal/fleet"
)

func TestWindowsAgentIsDiscoveredAndUsesWindowsWorkspacePaths(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.Fleet = fleet.New(config.Default().Fleet, deps.Cfg.Tools.Shell.Path, deps.Cfg.Tools.Shell.Env)
	deps.Fleet.SetAgentHosts([]fleet.Host{{
		Name: "office-pc", Desc: `Windows · D:\Projects\App`, Workdir: `D:\Projects\App`,
		Agent: true, Windows: true,
	}})
	readToolDefinition := (&readTool{d: deps}).Definition()
	properties := readToolDefinition.InputSchema.(map[string]any)["properties"].(map[string]any)
	target := properties["target"].(map[string]any)
	targets, ok := target["enum"].([]string)
	if !ok || len(targets) != 2 || targets[0] != "local" || targets[1] != "office-pc" {
		t.Fatalf("target choices = %#v", targets)
	}
	host, err := deps.resolveTarget("office-pc")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveReadPath(deps, host, deps.Paths.ForTarget(host.name), `src\main.go`)
	if err != nil || resolved != "D:/Projects/App/src/main.go" {
		t.Fatalf("Windows relative workspace path = %q, err = %v", resolved, err)
	}
	if _, err := resolveReadPath(deps, host, deps.Paths.ForTarget(host.name), `..\outside.txt`); err != nil {
		t.Fatalf("workspace-relative path escaping is rejected by the Windows Agent rather than the Hub: %v", err)
	}
}

func TestWindowsAgentRequiresExplicitPowerShellOptIn(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.Fleet = fleet.New(config.Default().Fleet, deps.Cfg.Tools.Shell.Path, deps.Cfg.Tools.Shell.Env)
	deps.Fleet.SetAgentHosts([]fleet.Host{{Name: "office-pc", Workdir: `D:\Projects\App`, Agent: true, Windows: true}})
	output, isError := call(t, &shellTool{d: deps}, map[string]any{
		"target": "office-pc", "command": "Get-ChildItem", "workdir": ".",
	})
	if !isError || !strings.Contains(output, "disabled") {
		t.Fatalf("PowerShell execution should be disabled by default: isError=%v output=%q", isError, output)
	}
	background, isError := call(t, &shellTool{d: deps}, map[string]any{
		"target": "office-pc", "command": "Get-ChildItem", "workdir": ".", "run_in_background": true,
	})
	if !isError || !strings.Contains(background, "disabled") {
		t.Fatalf("background PowerShell execution should be disabled by default: isError=%v output=%q", isError, background)
	}
}

func TestShellDescriptionMatchesWindowsAndUnixAgentExecutors(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.Fleet = fleet.New(config.Default().Fleet, deps.Cfg.Tools.Shell.Path, deps.Cfg.Tools.Shell.Env)
	deps.Fleet.SetAgentHosts([]fleet.Host{
		{Name: "office-pc", Workdir: `D:\Projects`, Agent: true, Windows: true, OS: "windows"},
		{Name: "build-linux", Workdir: "/srv/projects", Agent: true, OS: "linux"},
	})
	description := (&shellTool{d: deps}).Definition().Description
	for _, expected := range []string{
		"target=office-pc 使用 PowerShell", "pwsh.exe", "Windows PowerShell 5.1", "不要使用 && 或 ||",
		"target=build-linux 使用 Bash", "POSIX shell 兼容语法",
	} {
		if !strings.Contains(description, expected) {
			t.Errorf("shell description missing %q:\n%s", expected, description)
		}
	}
	if strings.Contains(description, "保持 POSIX sh 兼容") {
		t.Fatalf("generic POSIX instruction conflicts with the Windows Agent executor:\n%s", description)
	}
}

func TestQueryRefusesWindowsAgentShellMismatch(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.Fleet = fleet.New(config.Default().Fleet, deps.Cfg.Tools.Shell.Path, deps.Cfg.Tools.Shell.Env)
	deps.Fleet.SetAgentHosts([]fleet.Host{{Name: "office-pc", Workdir: `D:\Projects`, Agent: true, Windows: true, OS: "windows"}})
	output, isError := call(t, &queryTool{d: deps}, map[string]any{
		"target": "office-pc", "command": "Get-Process",
	})
	if !isError || !strings.Contains(output, "只支持 POSIX shell") {
		t.Fatalf("query did not explain its unsupported PowerShell target: isError=%v output=%q", isError, output)
	}
}
