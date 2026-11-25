package agentui

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omni-mcp/internal/agent"
	"omni-mcp/internal/fleet"
)

func TestServerSecurityAndCSRF(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent-ui.json")
	ctrl, err := NewController(cfgPath, "v0.2.0-test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	srv := NewServer(ctrl, slog.New(slog.DiscardHandler), nil)
	handler := srv.Handler()

	// 1. DNS rebinding host rejected
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback Host, got %d", rec.Code)
	}

	// 2. Unauthenticated API call rejected
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18485/api/state", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without session cookie, got %d", rec.Code)
	}

	// 3. Bootstrap with valid token sets HttpOnly SameSite=Strict cookie and redirects
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18485/?token="+srv.BootstrapToken(), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on bootstrap, got %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("expected strict HttpOnly session cookie, got %#v", cookies)
	}
	sessionCookie := cookies[0]

	// 4. POST without CSRF token rejected
	workDir := t.TempDir()
	body, _ := json.Marshal(ConfigUpdate{
		Hub:       "https://mcp.example.com",
		Name:      "office-pc",
		AllowDirs: []string{workDir},
		SaveToken: true,
		Token:     "super-secret-pairing-token-123456",
	})
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18485/api/config", bytes.NewReader(body))
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without X-Omni-UI-Token, got %d", rec.Code)
	}

	// 5. POST with valid CSRF succeeds and never leaks token in JSON response
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18485/api/config", bytes.NewReader(body))
	req.AddCookie(sessionCookie)
	req.Header.Set("X-Omni-UI-Token", srv.csrfToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid config save, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret-pairing-token-123456") {
		t.Fatalf("response leaked plaintext pairing token!")
	}
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if !snap.Profile.HasToken || snap.Profile.Name != "office-pc" {
		t.Fatalf("unexpected saved profile snapshot: %#v", snap.Profile)
	}
}

func TestControllerConnectOperationAuditAndDisconnect(t *testing.T) {
	manager := agent.NewManager(map[string]string{"office-pc": "pairing-token-xyz"}, slog.New(slog.DiscardHandler), nil)
	defer manager.Close()
	hubSrv := httptest.NewServer(manager)
	defer hubSrv.Close()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello omni ui\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "agent-ui.json")
	ctrl, err := NewController(cfgPath, "v0.2.0-test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	if err := ctrl.SaveConfig(ConfigUpdate{
		Hub:        hubSrv.URL,
		Name:       "office-pc",
		AllowDirs:  []string{workDir},
		AllowExec:  false,
		SaveToken:  true,
		Token:      "pairing-token-xyz",
		ConnectNow: true,
	}); err != nil {
		t.Fatalf("SaveConfig + ConnectNow failed: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().State == "connected" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if got := ctrl.Snapshot().State; got != "connected" {
		t.Fatalf("expected connected state, got %q (err=%q)", got, ctrl.Snapshot().LastError)
	}

	// Call fs.read through Hub and confirm it shows up in UI audit events
	var readRes fleet.AgentFileContent
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := manager.Call(ctx, "office-pc", "fs.read", fleet.AgentFileRead{Path: "hello.txt", MaxBytes: 1024}, &readRes); err != nil {
		t.Fatalf("Hub fs.read failed: %v", err)
	}
	if readRes.Data == "" {
		t.Fatalf("expected non-empty file data from fs.read")
	}

	snap := ctrl.Snapshot()
	foundOp := false
	for _, ev := range snap.Events {
		if ev.Kind == "operation" && ev.Method == "fs.read" && strings.Contains(ev.Summary, "hello.txt") {
			foundOp = true
			break
		}
	}
	if !foundOp {
		t.Fatalf("expected fs.read operation in UI audit events, got %#v", snap.Events)
	}

	// Emergency disconnect
	ctrl.Disconnect()
	if got := ctrl.Snapshot().State; got != "disconnected" {
		t.Fatalf("expected disconnected after Disconnect(), got %q", got)
	}
}

func TestControllerOneClickPairingAndAutoConnect(t *testing.T) {
	manager := agent.NewManager(nil, slog.New(slog.DiscardHandler), nil)
	defer manager.Close()
	if err := manager.AttachStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	var hubURL string
	mux := http.NewServeMux()
	mux.Handle("/agent/ws", manager)
	mux.HandleFunc("/agent/pair/start", func(w http.ResponseWriter, r *http.Request) {
		manager.HandlePairStart(hubURL)(w, r)
	})
	mux.HandleFunc("/agent/pair/poll", manager.HandlePairPoll())
	hubSrv := httptest.NewServer(mux)
	defer hubSrv.Close()
	hubURL = hubSrv.URL

	cfgPath := filepath.Join(t.TempDir(), "agent-ui.json")
	ctrl, err := NewController(cfgPath, "v0.2.0-test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetNoOpen(true)
	defer ctrl.Close()

	// Save with custom device name "mypc" and NO manual token -> Connect automatically enters pairing mode
	if err := ctrl.SaveConfig(ConfigUpdate{
		Hub:        hubSrv.URL,
		Name:       "mypc",
		AllowDirs:  []string{t.TempDir()},
		SaveToken:  true,
		ConnectNow: true,
	}); err != nil {
		t.Fatalf("SaveConfig + ConnectNow (pairing) failed: %v", err)
	}

	snap := ctrl.Snapshot()
	if snap.State != "pairing" || snap.Pairing == nil || snap.Pairing.Code == "" {
		t.Fatalf("expected pairing state with verification code, got state=%q pairing=%+v", snap.State, snap.Pairing)
	}

	// Hub admin clicks Approve
	if _, err := manager.ApprovePairing(snap.Pairing.Code, "admin"); err != nil {
		t.Fatalf("ApprovePairing: %v", err)
	}

	// Controller polls, saves sealed token bound to "mypc", and auto-connects
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().State == "connected" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	finalSnap := ctrl.Snapshot()
	if finalSnap.State != "connected" || !finalSnap.Profile.HasToken || finalSnap.Profile.PairedName != "mypc" {
		t.Fatalf("expected connected paired state for mypc, got state=%q profile=%+v err=%q", finalSnap.State, finalSnap.Profile, finalSnap.LastError)
	}
}
