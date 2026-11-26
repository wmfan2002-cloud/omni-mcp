package agentui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omni-mcp/internal/agent"
)

func TestGatewayHelpers(t *testing.T) {
	if err := agent.ValidateName(defaultDeviceName()); err != nil {
		t.Fatalf("defaultDeviceName produced invalid name: %v", err)
	}

	got, err := normalizeNgrokDomain("https://My-PC.ngrok-free.app/")
	if err != nil || got != "my-pc.ngrok-free.app" {
		t.Fatalf("normalizeNgrokDomain = (%q, %v), want my-pc.ngrok-free.app", got, err)
	}
	if _, err := normalizeNgrokDomain("my-pc.ngrok-free.app/mcp"); err == nil {
		t.Fatal("expected error for ngrok domain with path")
	}

	pub, err := validatePublicHTTPSURL("https://mcp.example.com/")
	if err != nil || pub != "https://mcp.example.com" {
		t.Fatalf("validatePublicHTTPSURL = (%q, %v)", pub, err)
	}
	if _, err := validatePublicHTTPSURL("http://mcp.example.com"); err == nil {
		t.Fatal("expected error for non-https public URL")
	}

	for _, tc := range []struct {
		os, arch string
		wantTgz  bool
	}{
		{"windows", "amd64", false},
		{"darwin", "arm64", true},
		{"darwin", "amd64", true},
		{"linux", "amd64", false},
		{"linux", "arm64", false},
	} {
		u, isTgz, err := cloudflaredDownloadSpec(tc.os, tc.arch)
		if err != nil || !strings.HasPrefix(u, "https://github.com/cloudflare/cloudflared/") || isTgz != tc.wantTgz {
			t.Fatalf("cloudflaredDownloadSpec(%s,%s) = (%q, %v, %v)", tc.os, tc.arch, u, isTgz, err)
		}
	}
}

func TestLocalGatewayEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sampleFile := filepath.Join(workDir, "hello.txt")
	if err := os.WriteFile(sampleFile, []byte("hello from omni-mcp local gateway\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(tmp, "agent-ui.json")
	ctrl, err := NewController(cfgPath, "test-v0.4", nil)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	ctrl.SetNoOpen(true)
	defer ctrl.Close()

	zeroPort := 0
	randPort := true
	err = ctrl.SaveConfig(ConfigUpdate{
		Mode:              ModeGateway,
		Name:              "test-local-pc",
		AllowDirs:         []string{workDir},
		AllowExec:         false,
		SaveToken:         true,
		GatewayPort:       &zeroPort,
		GatewayRandomPort: &randPort,
		GatewayTunnel:     TunnelLocal,
		StartGatewayNow:   true,
	})
	if err != nil {
		t.Fatalf("SaveConfig(gateway): %v", err)
	}

	// Wait until local gateway reaches "running"
	deadline := time.Now().Add(15 * time.Second)
	var snap Snapshot
	for time.Now().Before(deadline) {
		snap = ctrl.Snapshot()
		if snap.Gateway != nil && snap.Gateway.State == "running" {
			break
		}
		if snap.Gateway != nil && snap.Gateway.State == "error" {
			t.Fatalf("gateway entered error state: %s", snap.Gateway.LastError)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if snap.Gateway == nil || snap.Gateway.State != "running" {
		t.Fatalf("gateway did not reach running state: %+v", snap.Gateway)
	}
	if !strings.HasSuffix(snap.Gateway.MCPEndpoint, "/mcp") {
		t.Fatalf("unexpected MCPEndpoint: %q", snap.Gateway.MCPEndpoint)
	}

	creds, err := ctrl.RevealGatewayCredentials()
	if err != nil {
		t.Fatalf("RevealGatewayCredentials: %v", err)
	}
	if creds.Username != "admin" || creds.Password == "" || !strings.HasPrefix(creds.BearerToken, "omni_gw_") {
		t.Fatalf("unexpected credentials: %+v", creds)
	}

	// Verify Snapshot JSON never leaks password or bearer token
	snapJSON, _ := json.Marshal(snap)
	if strings.Contains(string(snapJSON), creds.Password) || strings.Contains(string(snapJSON), creds.BearerToken) {
		t.Fatal("Snapshot JSON leaked local gateway credentials")
	}

	// 1. Verify OAuth 2.1 discovery metadata matches PublicURL
	metaResp, err := http.Get(snap.Gateway.PublicURL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("GET oauth metadata: %v", err)
	}
	defer metaResp.Body.Close()
	if metaResp.StatusCode != http.StatusOK {
		t.Fatalf("oauth metadata status = %d", metaResp.StatusCode)
	}
	var meta map[string]any
	if err := json.NewDecoder(metaResp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode oauth metadata: %v", err)
	}
	if meta["issuer"] != snap.Gateway.PublicURL {
		t.Fatalf("issuer = %v, want %s", meta["issuer"], snap.Gateway.PublicURL)
	}

	// 2. Verify unauthenticated MCP initialize works (Gemini custom connector compatibility)
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	initReq, _ := http.NewRequest(http.MethodPost, snap.Gateway.MCPEndpoint, strings.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initResp, err := http.DefaultClient.Do(initReq)
	if err != nil {
		t.Fatalf("initialize request: %v", err)
	}
	_ = initResp.Body.Close()
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("unauthenticated initialize status = %d, want 200", initResp.StatusCode)
	}

	// 3. Verify authenticated tools/call read works without specifying target (DefaultTarget auto-routes!)
	callTool := func(bearer, path string) (*http.Response, string) {
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "read",
				"arguments": map[string]any{
					"path": path,
				},
			},
		})
		req, _ := http.NewRequest(http.MethodPost, snap.Gateway.MCPEndpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("tools/call read: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(raw)
	}

	resp, body := callTool(creds.BearerToken, sampleFile)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read tool status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "hello from omni-mcp local gateway") {
		t.Fatalf("read tool response did not contain file contents: %s", body)
	}

	// 4. Verify audit event was captured in Desktop Console feed
	snapAfter := ctrl.Snapshot()
	foundOp := false
	for _, ev := range snapAfter.Events {
		if ev.Kind == "operation" {
			foundOp = true
			break
		}
	}
	if !foundOp {
		t.Fatalf("expected live operation event in console feed, got: %+v", snapAfter.Events)
	}

	// 5. Verify credential rotation invalidates old bearer token and accepts new one
	newCreds, err := ctrl.RotateGatewayCredentials()
	if err != nil {
		t.Fatalf("RotateGatewayCredentials: %v", err)
	}
	if newCreds.BearerToken == creds.BearerToken || newCreds.Password == creds.Password {
		t.Fatal("RotateGatewayCredentials did not generate fresh credentials")
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		snap = ctrl.Snapshot()
		if snap.Gateway != nil && snap.Gateway.State == "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if snap.Gateway == nil || snap.Gateway.State != "running" {
		t.Fatalf("gateway did not restart cleanly after rotate: %+v", snap.Gateway)
	}
	oldResp, _ := callTool(creds.BearerToken, sampleFile)
	if oldResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old bearer token status = %d, want 401", oldResp.StatusCode)
	}
	newResp, newBody := callTool(newCreds.BearerToken, sampleFile)
	if newResp.StatusCode != http.StatusOK || !strings.Contains(newBody, "hello from omni-mcp local gateway") {
		t.Fatalf("new bearer token failed: status=%d body=%s", newResp.StatusCode, newBody)
	}

	ctrl.StopGateway()
	if s := ctrl.Snapshot(); s.Gateway.State != "stopped" {
		t.Fatalf("expected stopped state, got %q", s.Gateway.State)
	}
}

func TestGatewayServerRoutesAndIcon256(t *testing.T) {
	tmp := t.TempDir()
	ctrl, err := NewController(filepath.Join(tmp, "agent-ui.json"), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	srv := NewServer(ctrl, nil, nil)
	h := srv.Handler()

	// Verify /icon-256.png serves PNG bytes
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/icon-256.png", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() < 100 {
		t.Fatalf("/icon-256.png failed: code=%d ct=%q len=%d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}

	// Verify /api/gateway/reveal requires session + CSRF
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/gateway/reveal", nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated /api/gateway/reveal = %d, want 403", rec2.Code)
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/gateway/reveal", nil)
	req3.AddCookie(&http.Cookie{Name: sessionCookieName, Value: srv.BootstrapToken()})
	req3.Header.Set("X-Omni-UI-Token", srv.csrfToken)
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("authenticated /api/gateway/reveal = %d, body=%s", rec3.Code, rec3.Body.String())
	}
	var creds GatewayCredentials
	if err := json.Unmarshal(rec3.Body.Bytes(), &creds); err != nil || creds.Password == "" || creds.BearerToken == "" {
		t.Fatalf("unexpected reveal response: %s", rec3.Body.String())
	}
}

func TestLiveCloudflareQuickTunnel(t *testing.T) {
	if os.Getenv("OMNI_LIVE_CF_TEST") != "1" {
		t.Skip("set OMNI_LIVE_CF_TEST=1 to run live Cloudflare Quick Tunnel test")
	}
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sample := filepath.Join(workDir, "probe.txt")
	if err := os.WriteFile(sample, []byte("LIVE_CF_TUNNEL_OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctrl, err := NewController(filepath.Join(tmp, "agent-ui.json"), "v0.4.0-live", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetNoOpen(true)
	defer ctrl.Close()

	z := 0
	tr := true
	if err := ctrl.SaveConfig(ConfigUpdate{
		Mode:              ModeGateway,
		Name:              "cf-live-check",
		AllowDirs:         []string{workDir},
		GatewayPort:       &z,
		GatewayRandomPort: &tr,
		GatewayTunnel:     TunnelCloudflareQuick,
		StartGatewayNow:   true,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(45 * time.Second)
	var snap Snapshot
	for time.Now().Before(deadline) {
		snap = ctrl.Snapshot()
		if snap.Gateway != nil && (snap.Gateway.State == "running" || snap.Gateway.State == "error") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if snap.Gateway == nil || snap.Gateway.State != "running" {
		t.Fatalf("gateway failed: %+v", snap.Gateway)
	}
	t.Logf("Live Cloudflare Quick Tunnel MCP Endpoint: %s", snap.Gateway.MCPEndpoint)

	creds, err := ctrl.RevealGatewayCredentials()
	if err != nil {
		t.Fatal(err)
	}
	var metaBody string
	for i := 0; i < 8; i++ {
		resp, err := http.Get(snap.Gateway.PublicURL + "/.well-known/oauth-authorization-server")
		if err == nil && resp.StatusCode == http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			metaBody = string(b)
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(metaBody, snap.Gateway.PublicURL) {
		t.Fatalf("public OAuth metadata did not match %s: %s", snap.Gateway.PublicURL, metaBody)
	}

	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "read",
			"arguments": map[string]any{"path": sample},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, snap.Gateway.MCPEndpoint, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+creds.BearerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "LIVE_CF_TUNNEL_OK") {
		t.Fatalf("public MCP read failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
}
