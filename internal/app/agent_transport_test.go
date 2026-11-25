package app_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"omni-mcp/internal/app"
	"omni-mcp/internal/config"
)

func TestBuildHandlerSupportsAgentWebSocketHijacking(t *testing.T) {
	t.Setenv("OMNI_AGENT_TOKEN_TEST_AGENT", "app-agent-pairing-token")
	cfg := config.Default()
	cfg.Server.BaseURL = "https://mcp.example.com"
	cfg.Server.DataDir = t.TempDir()
	cfg.Auth.Enabled = false
	built, err := app.Build(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	server := httptest.NewServer(built.Handler)
	defer server.Close()
	header := http.Header{
		"Authorization":     []string{"Bearer app-agent-pairing-token"},
		"X-Omni-Agent-Name": []string{"test-agent"},
	}
	conn, response, err := websocket.Dial(context.Background(), server.URL+"/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("Agent WebSocket handshake failed (response=%#v): %v", response, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	if err := wsjson.Write(context.Background(), conn, map[string]any{
		"type": "hello", "name": "test-agent", "version": "test", "os": runtime.GOOS,
		"shell": "test shell", "workdir": t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	var registration struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(context.Background(), conn, &registration); err != nil {
		t.Fatal(err)
	}
	if registration.Type != "ready" {
		t.Fatalf("Agent registration response = %#v", registration)
	}
}
