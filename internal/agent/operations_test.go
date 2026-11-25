package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"log/slog"
	"omni-mcp/internal/fleet"
)

func newExecutableClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{
		Hub: "https://hub.example.com", Token: "test-token", Name: "test-agent",
		AllowDirs: []string{t.TempDir()}, AllowExec: true, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestCompletedBackgroundProcessIsCollectedAfterFinalPoll(t *testing.T) {
	client := newExecutableClient(t)
	command := "printf omni-done"
	if runtime.GOOS == "windows" {
		command = "Write-Output omni-done"
	}
	started, err := client.startCommand(context.Background(), fleet.AgentRunRequest{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	proc := client.procs[started.ID]
	client.mu.Unlock()
	if proc == nil {
		t.Fatal("started process was not tracked")
	}
	select {
	case <-proc.done:
	case <-time.After(5 * time.Second):
		t.Fatal("background process did not finish")
	}
	result, err := client.pollProcess(started.ID)
	if err != nil || result.Running || !strings.Contains(result.Output, "omni-done") {
		t.Fatalf("final poll = %#v, err = %v", result, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		remaining := len(client.procs)
		client.mu.Unlock()
		if remaining == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("completed process record was not removed")
}

func TestDisconnectedSessionCleanupStopsAndForgetsBackgroundProcesses(t *testing.T) {
	manager := NewManager(map[string]string{"test-agent": "test-token"}, slog.New(slog.DiscardHandler), nil)
	server := newHubTestServer(manager)
	defer server.Close()
	client, err := NewClient(ClientOptions{
		Hub: server.URL, Token: "test-token", Name: "test-agent", AllowDirs: []string{t.TempDir()},
		AllowExec: true, Log: slog.New(slog.DiscardHandler),
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
	if len(manager.Infos()) != 1 {
		cancel()
		t.Fatal("Agent session did not register")
	}
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 30"
	}
	var started fleet.AgentStartResult
	requestCtx, cancelRequest := context.WithTimeout(ctx, 3*time.Second)
	err = manager.Call(requestCtx, "test-agent", "shell.start", fleet.AgentRunRequest{Command: command}, &started)
	cancelRequest()
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	proc := client.procs[started.ID]
	client.mu.Unlock()
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnected Agent session did not stop")
	}
	select {
	case <-proc.done:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnected Agent did not stop its child process")
	}
	client.mu.Lock()
	remaining := len(client.procs)
	client.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("disconnected Agent still tracks %d processes", remaining)
	}
}
