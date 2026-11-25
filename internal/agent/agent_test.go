package agent

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log/slog"
	"omni-mcp/internal/fleet"
)

func newTestClient(t *testing.T, directories ...string) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{
		Hub: "https://hub.example.com", Token: "shared-development-token", Name: "office-pc",
		AllowDirs: directories, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func newHubTestServer(manager *Manager) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(manager.ServeHTTP))
}

func TestAgentEndpointRequiresSecureRemoteHub(t *testing.T) {
	valid, err := agentEndpoint("https://mcp.example.com")
	if err != nil || valid != "https://mcp.example.com/agent/ws" {
		t.Fatalf("endpoint = %q, err = %v", valid, err)
	}
	if valid, err = agentEndpoint("http://127.0.0.1:8080"); err != nil || valid != "http://127.0.0.1:8080/agent/ws" {
		t.Fatalf("loopback endpoint = %q, err = %v", valid, err)
	}
	for _, address := range []string{
		"http://hub.example.com", "https://hub.example.com/private", "https://user:password@hub.example.com",
		"https://hub.example.com?token=secret", "https://hub.example.com/#unsafe",
	} {
		if _, err := agentEndpoint(address); err == nil {
			t.Errorf("agentEndpoint(%q) unexpectedly succeeded", address)
		}
	}
}

func TestClientCannotEscapeRegisteredRoots(t *testing.T) {
	rootPath := t.TempDir()
	outside := t.TempDir()
	root := newTestClient(t, rootPath)
	insideFile := filepath.Join(rootPath, "README.md")
	if err := os.WriteFile(insideFile, []byte("workspace ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := root.resolve(filepath.Join(rootPath, "..", filepath.Base(outside), "secret.txt")); err == nil {
		t.Fatal("parent traversal escaped the registered root")
	}
	symlink := filepath.Join(rootPath, "outside")
	if err := os.Symlink(outside, symlink); err == nil {
		if _, err := root.readFile(filepath.Join(symlink, "secret.txt"), 1024, false); err == nil {
			t.Fatal("symbolic link escaped the registered root")
		}
	}
	result, err := root.readFile(insideFile, 1024, false)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil || string(decoded) != "workspace ok\n" {
		t.Fatalf("file content = %q, err = %v", decoded, err)
	}
}

func TestAgentFileWriteSearchAndGlobRespectWorkspace(t *testing.T) {
	rootPath := t.TempDir()
	client := newTestClient(t, rootPath)
	if err := client.writeFile(fleet.AgentFileWrite{Path: "src/main.go", Data: base64.StdEncoding.EncodeToString([]byte("package main\n// TODO improve\n"))}); err != nil {
		t.Fatal(err)
	}
	result, err := client.search(context.Background(), fleet.AgentSearchRequest{
		Path: rootPath, Pattern: "TODO", MaxResults: 10, IgnoreDirs: []string{"node_modules"},
	})
	if err != nil || result.Engine != "Omni MCP Agent" || len(result.Lines) != 1 {
		t.Fatalf("search result = %#v, err = %v", result, err)
	}
	globbed, err := client.glob(context.Background(), fleet.AgentGlobRequest{Path: rootPath, Pattern: "**/*.go", MaxResults: 10})
	if err != nil || len(globbed.Files) != 1 || !strings.HasSuffix(globbed.Files[0].Path, "main.go") {
		t.Fatalf("glob result = %#v, err = %v", globbed, err)
	}
	if _, err := client.readFile("../outside.txt", 1024, false); err == nil {
		t.Fatal("file read escaped the workspace")
	}
}

func TestAgentProcessEnvironmentDoesNotAcceptSecrets(t *testing.T) {
	environment := commandEnvironment(map[string]string{
		"PATH": "C:\\allowed", "CUSTOM_MODE": "test", "API_TOKEN": "must-not-leak", "Bad Name": "drop",
	})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "must-not-leak") || strings.Contains(joined, "API_TOKEN=") {
		t.Fatalf("sensitive variables leaked into command environment: %s", joined)
	}
	if !strings.Contains(joined, "CUSTOM_MODE=test") || !strings.Contains(joined, "PATH=C:\\allowed") {
		t.Fatalf("allowed command environment values were lost: %s", joined)
	}
}

func TestAgentWebSocketPairsAndDispatchesTools(t *testing.T) {
	rootPath := t.TempDir()
	manager := NewManager(map[string]string{"office-pc": "pairing-key"}, slog.New(slog.DiscardHandler), nil)
	server := newHubTestServer(manager)
	t.Cleanup(server.Close)
	client, err := NewClient(ClientOptions{
		Hub:   server.URL,
		Token: "pairing-key", Name: "office-pc", Version: "0.1.0", AllowDirs: []string{rootPath},
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.runSession(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for len(manager.Infos()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if infos := manager.Infos(); len(infos) != 1 || infos[0].Name != "office-pc" || infos[0].Version != "0.1.0" {
		t.Fatalf("registered devices = %#v", infos)
	}
	var health systemInfo
	requestContext, requestCancel := context.WithTimeout(ctx, time.Second)
	defer requestCancel()
	if err := manager.Call(requestContext, "office-pc", "system.info", map[string]any{}, &health); err != nil {
		t.Fatal(err)
	}
	if health.Name != "office-pc" || health.Workdir != rootPath || health.OS != runtime.GOOS {
		t.Fatalf("health response = %#v", health)
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("expected disconnected Agent session")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Agent session did not stop")
	}
}

func TestHubRejectsInvalidAgentNamesAndUnsupportedTargets(t *testing.T) {
	server := newHubTestServer(NewManager(map[string]string{"office-pc": "pairing-key"}, slog.New(slog.DiscardHandler), nil))
	defer server.Close()
	endpoint, err := agentEndpoint(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []message{
		{Type: "hello", Name: "local", OS: "windows", Workdir: `C:\Projects`},
		{Type: "hello", Name: "WindowsHost", OS: "windows", Workdir: `C:\Projects`},
		{Type: "hello", Name: "office-pc", OS: "plan9", Workdir: "/projects"},
		{Type: "hello", Name: "office-pc", OS: "windows", Workdir: "relative/project"},
	} {
		header := http.Header{"Authorization": []string{"Bearer pairing-key"}, "X-Omni-Agent-Name": []string{"office-pc"}}
		conn, _, err := websocket.Dial(context.Background(), endpoint, &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(context.Background(), conn, candidate); err != nil {
			t.Fatal(err)
		}
		var rejected message
		if err := wsjson.Read(context.Background(), conn, &rejected); err != nil {
			t.Fatal(err)
		}
		if rejected.Type != "reject" || rejected.Error == "" {
			t.Errorf("Agent hello %#v was not rejected: %#v", candidate, rejected)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}
}

func TestHubRequiresBearerBeforeWebSocketUpgrade(t *testing.T) {
	server := newHubTestServer(NewManager(map[string]string{"office-pc": "pairing-key"}, slog.New(slog.DiscardHandler), nil))
	defer server.Close()
	endpoint, err := agentEndpoint(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, httpResponse, err := websocket.Dial(context.Background(), endpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Omni-Agent-Name": []string{"office-pc"}},
	})
	if err == nil {
		response.Close(websocket.StatusNormalClosure, "")
		t.Fatal("Hub accepted a connection without a pairing token")
	}
	if httpResponse == nil || httpResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized handshake response = %#v, err = %v", httpResponse, err)
	}
}

func TestHubAcceptsWindowsAgentRegistration(t *testing.T) {
	manager := NewManager(map[string]string{"office-pc": "office-secret"}, slog.New(slog.DiscardHandler), nil)
	server := newHubTestServer(manager)
	defer server.Close()
	endpoint, err := agentEndpoint(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{
		"Authorization":     []string{"Bearer office-secret"},
		"X-Omni-Agent-Name": []string{"office-pc"},
	}
	conn, _, err := websocket.Dial(context.Background(), endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	if err := wsjson.Write(context.Background(), conn, message{
		Type: "hello", Name: "office-pc", Version: "test", OS: "windows", Shell: "PowerShell",
		Workdir: `D:\Projects`, AllowExec: true,
	}); err != nil {
		t.Fatal(err)
	}
	var ready message
	if err := wsjson.Read(context.Background(), conn, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != "ready" {
		t.Fatalf("Hub registration response = %#v", ready)
	}
	infos := manager.Infos()
	if len(infos) != 1 || infos[0].OS != "windows" || infos[0].Workdir != `D:\Projects` {
		t.Fatalf("Windows Agent registration = %#v", infos)
	}
}

func TestAgentPairingTokensAreScopedToNames(t *testing.T) {
	manager := NewManager(map[string]string{
		"office-pc": "office-secret",
		"lab-pc":    "lab-secret",
	}, slog.New(slog.DiscardHandler), nil)
	server := newHubTestServer(manager)
	defer server.Close()
	client, err := NewClient(ClientOptions{
		Hub: server.URL, Token: "office-secret", Name: "lab-pc", AllowDirs: []string{t.TempDir()},
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.runSession(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a device token authenticated a different name: %v", err)
	}
	if len(manager.Infos()) != 0 {
		t.Fatalf("invalid token registered a device: %#v", manager.Infos())
	}
}

func TestHubRejectsDuplicateOnlineAgentWithoutReplacingCurrentSession(t *testing.T) {
	rootPath := t.TempDir()
	manager := NewManager(map[string]string{"office-pc": "office-secret"}, slog.New(slog.DiscardHandler), nil)
	server := newHubTestServer(manager)
	defer server.Close()
	first, err := NewClient(ClientOptions{
		Hub: server.URL, Token: "office-secret", Name: "office-pc", AllowDirs: []string{rootPath},
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	finished := make(chan error, 1)
	go func() { finished <- first.runSession(firstCtx) }()
	deadline := time.Now().Add(3 * time.Second)
	for len(manager.Infos()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(manager.Infos()) != 1 {
		t.Fatal("first Agent did not register")
	}
	second, err := NewClient(ClientOptions{
		Hub: server.URL, Token: "office-secret", Name: "office-pc", AllowDirs: []string{rootPath},
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	if err := second.runSession(secondCtx); err == nil || !strings.Contains(err.Error(), "already connected") {
		t.Fatalf("duplicate Agent connection was not rejected: %v", err)
	}
	if infos := manager.Infos(); len(infos) != 1 || infos[0].Name != "office-pc" {
		t.Fatalf("duplicate connection replaced the first Agent: %#v", infos)
	}
	var health systemInfo
	requestContext, cancelRequest := context.WithTimeout(context.Background(), time.Second)
	defer cancelRequest()
	if err := manager.Call(requestContext, "office-pc", "system.info", map[string]any{}, &health); err != nil {
		t.Fatalf("the first Agent session was interrupted by a duplicate: %v", err)
	}
	cancelFirst()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("first Agent session did not stop")
	}
}

func TestRetryDelayResetsAfterStableSession(t *testing.T) {
	if got := retryDelayAfterSession(30*time.Second, 29*time.Second); got != 30*time.Second {
		t.Fatalf("unstable session reset retry delay: %v", got)
	}
	if got := retryDelayAfterSession(30*time.Second, 30*time.Second); got != time.Second {
		t.Fatalf("stable session retry delay = %v, want 1s", got)
	}
	if got := nextRetryDelay(20 * time.Second); got != 30*time.Second {
		t.Fatalf("retry delay cap = %v, want 30s", got)
	}
}

func TestCommandArgsFollowOperatingSystem(t *testing.T) {
	lookups := map[string]string{"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`, "bash": "/usr/bin/bash"}
	lookup := func(name string) (string, error) {
		if resolved, ok := lookups[name]; ok {
			return resolved, nil
		}
		return "", os.ErrNotExist
	}

	powershell := commandArgsForOS("windows", "Get-ChildItem; Write-Output 'ok'", lookup)
	if powershell[0] != lookups["pwsh.exe"] || powershell[4] != "-EncodedCommand" {
		t.Fatalf("Windows shell args = %#v", powershell)
	}
	encoded, err := base64.StdEncoding.DecodeString(powershell[5])
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(encoded)/2)
	for index := range units {
		units[index] = uint16(encoded[index*2]) | uint16(encoded[index*2+1])<<8
	}
	if script := string(utf16.Decode(units)); !strings.Contains(script, "Get-ChildItem") || !strings.Contains(script, "Write-Output") {
		t.Fatalf("PowerShell script = %q", script)
	}
	unix := commandArgsForOS("linux", "printf ok", lookup)
	if len(unix) != 3 || unix[0] != "/usr/bin/bash" || unix[1] != "-c" || unix[2] != "printf ok" {
		t.Fatalf("Linux shell args = %#v", unix)
	}
}

func TestPerAgentTokenEnvironmentNames(t *testing.T) {
	name, err := TokenEnvName("office-pc")
	if err != nil || name != "OMNI_AGENT_TOKEN_OFFICE_PC" {
		t.Fatalf("token env name = %q, err = %v", name, err)
	}
	tokens := TokensFromEnvironment([]string{
		"OMNI_AGENT_TOKEN_OFFICE_PC=office-secret",
		"OMNI_AGENT_TOKEN_LAB_PC=lab-secret",
		"OMNI_AGENT_TOKEN=legacy-shared-secret",
		"OMNI_AGENT_TOKEN_1_BAD=ignored",
	})
	if len(tokens) != 2 || tokens["office-pc"] != "office-secret" || tokens["lab-pc"] != "lab-secret" {
		t.Fatalf("loaded Agent tokens = %#v", tokens)
	}
}
