package app

import (
	"log/slog"
	"slices"
	"testing"

	"omni-mcp/internal/config"
)

func TestBuildRegistersServersToolWhenAgentTokenConfigured(t *testing.T) {
	t.Setenv("OMNI_AGENT_TOKEN_OFFICE_PC", "test-agent-secret-token")
	cfg := config.Default()
	cfg.Server.DataDir = t.TempDir()
	cfg.Auth.Enabled = false
	built, err := Build(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	defer built.Close()
	if !slices.Contains(built.Tools, "servers") {
		t.Fatalf("expected servers tool when OMNI_AGENT_TOKEN_* is set, got %v", built.Tools)
	}
}
