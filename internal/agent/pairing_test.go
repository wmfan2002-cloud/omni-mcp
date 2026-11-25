package agent

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPairingLifecycleAndRevocation(t *testing.T) {
	dataDir := t.TempDir()
	mgr := NewManager(nil, slog.New(slog.DiscardHandler), nil)
	defer mgr.Close()
	if err := mgr.AttachStore(dataDir); err != nil {
		t.Fatalf("AttachStore: %v", err)
	}
	if !mgr.Enabled() {
		t.Fatal("Manager should be enabled when dynamic pairing store is attached")
	}

	startRes, err := mgr.StartPairing("mypc", "windows", "127.0.0.1", "https://mcp.example.com")
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}
	if len(startRes.Code) != 9 || startRes.Code[4] != '-' {
		t.Fatalf("unexpected pairing code format: %q", startRes.Code)
	}
	if startRes.ApproveURL != "https://mcp.example.com/agents?code="+startRes.Code {
		t.Fatalf("unexpected approve URL: %q", startRes.ApproveURL)
	}

	// Still pending before approval
	if got := mgr.PollPairing(startRes.Code, startRes.PollToken); got.Status != "pending" {
		t.Fatalf("expected pending, got %+v", got)
	}

	// Approve on Hub
	if _, err := mgr.ApprovePairing(startRes.Code, "admin"); err != nil {
		t.Fatalf("ApprovePairing: %v", err)
	}

	// Poll receives plaintext token once
	approved := mgr.PollPairing(startRes.Code, startRes.PollToken)
	if approved.Status != "approved" || approved.Token == "" || approved.Name != "mypc" {
		t.Fatalf("expected approved with token, got %+v", approved)
	}
	// Second poll must NOT leak token again
	if again := mgr.PollPairing(startRes.Code, startRes.PollToken); again.Status != "expired" || again.Token != "" {
		t.Fatalf("expected expired on second poll, got %+v", again)
	}

	// Real WebSocket agent connects with the issued token
	srv := httptest.NewServer(mgr)
	defer srv.Close()

	client, err := NewClient(ClientOptions{
		Hub:       srv.URL,
		Token:     approved.Token,
		Name:      "mypc",
		Version:   "v0.2.0-test",
		AllowDirs: []string{t.TempDir()},
		NoPrompt:  true,
		Log:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	isOnline := func() bool {
		for _, d := range mgr.ListDevices() {
			if d.Name == "mypc" && d.Online {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if isOnline() {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !isOnline() {
		t.Fatal("paired device failed to connect to Hub")
	}

	// Reloading from disk preserves hashed authorization
	reloaded := NewManager(nil, slog.New(slog.DiscardHandler), nil)
	defer reloaded.Close()
	if err := reloaded.AttachStore(dataDir); err != nil {
		t.Fatalf("reloaded AttachStore: %v", err)
	}
	devs := reloaded.ListDevices()
	if len(devs) != 1 || devs[0].Name != "mypc" || devs[0].Source != "hub" {
		t.Fatalf("unexpected reloaded devices: %+v", devs)
	}

	// Revoke disconnects active device and removes persistence
	if err := mgr.RevokeDevice("mypc"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	cancel()

	req := httptest.NewRequest(http.MethodGet, srv.URL+"/agent/ws", nil)
	req.Header.Set("Authorization", "Bearer "+approved.Token)
	req.Header.Set("X-Omni-Agent-Name", "mypc")
	rec := httptest.NewRecorder()
	mgr.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked token to be rejected, got HTTP %d", rec.Code)
	}
}

func TestPairingRejectAndReservedName(t *testing.T) {
	mgr := NewManager(nil, slog.New(slog.DiscardHandler), nil)
	defer mgr.Close()
	if err := mgr.AttachStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	mgr.ReserveNames([]string{"srv2"})

	if _, err := mgr.StartPairing("srv2", "linux", "127.0.0.1", "https://mcp.example.com"); err == nil {
		t.Fatal("expected reserved SSH host name to be rejected")
	}
	if _, err := mgr.StartPairing("local", "linux", "127.0.0.1", "https://mcp.example.com"); err == nil {
		t.Fatal("expected local name to be rejected")
	}

	startRes, err := mgr.StartPairing("laptop-1", "darwin", "127.0.0.1", "https://mcp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.RejectPairing(startRes.Code); err != nil {
		t.Fatalf("RejectPairing: %v", err)
	}
	if got := mgr.PollPairing(startRes.Code, startRes.PollToken); got.Status != "rejected" {
		t.Fatalf("expected rejected, got %+v", got)
	}
}
