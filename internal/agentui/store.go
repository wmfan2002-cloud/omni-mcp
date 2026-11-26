package agentui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Profile stores user-configurable Agent UI settings. The pairing token is stored
// sealed (DPAPI on Windows) and is never serialized back to browser responses.
type Profile struct {
	Hub             string   `json:"hub"`
	Name            string   `json:"name"`
	AllowDirs       []string `json:"allow_dirs"`
	AllowExec       bool     `json:"allow_exec"`
	AutoConnect     bool     `json:"auto_connect"`
	SaveToken       bool     `json:"save_token"`
	TokenSealed     string   `json:"token_sealed,omitempty"`
	TokenDeviceName string   `json:"token_device_name,omitempty"`

	// Local Standalone Gateway Mode settings (zero-VPS mode).
	Mode                    string `json:"mode,omitempty"` // remote | gateway
	GatewayPort             int    `json:"gateway_port,omitempty"`
	GatewayRandomPort       bool   `json:"gateway_random_port,omitempty"`
	GatewayTunnel           string `json:"gateway_tunnel,omitempty"` // cloudflare_quick | ngrok | cloudflare_named | local
	GatewayNgrokDomain      string `json:"gateway_ngrok_domain,omitempty"`
	GatewayNgrokTokenSealed string `json:"gateway_ngrok_token_sealed,omitempty"`
	GatewayCFPublicURL      string `json:"gateway_cf_public_url,omitempty"`
	GatewayCFTokenSealed    string `json:"gateway_cf_token_sealed,omitempty"`
	GatewayPasswordSealed   string `json:"gateway_password_sealed,omitempty"`
	GatewayBearerSealed     string `json:"gateway_bearer_sealed,omitempty"`
}

// PublicProfile is the sanitized view safe to send to the browser UI.
type PublicProfile struct {
	Hub         string   `json:"hub"`
	Name        string   `json:"name"`
	AllowDirs   []string `json:"allow_dirs"`
	AllowExec   bool     `json:"allow_exec"`
	AutoConnect bool     `json:"auto_connect"`
	SaveToken   bool     `json:"save_token"`
	HasToken    bool     `json:"has_token"`
	PairedName  string   `json:"paired_name,omitempty"`

	Mode               string `json:"mode"`
	GatewayPort        int    `json:"gateway_port"`
	GatewayRandomPort  bool   `json:"gateway_random_port"`
	GatewayTunnel      string `json:"gateway_tunnel"`
	GatewayNgrokDomain string `json:"gateway_ngrok_domain,omitempty"`
	GatewayCFPublicURL string `json:"gateway_cf_public_url,omitempty"`
	HasNgrokToken      bool   `json:"has_ngrok_token"`
	HasCFToken         bool   `json:"has_cf_token"`
	HasGatewayCreds    bool   `json:"has_gateway_creds"`
}

func (p Profile) tokenMatchesName() bool {
	return strings.TrimSpace(p.TokenDeviceName) == "" || p.TokenDeviceName == p.Name
}

func (p Profile) Public(hasRuntimeToken, hasRuntimeNgrok, hasRuntimeCF, hasRuntimeGatewayCreds bool) PublicProfile {
	dirs := append([]string(nil), p.AllowDirs...)
	if dirs == nil {
		dirs = []string{}
	}
	hasSealed := strings.TrimSpace(p.TokenSealed) != "" && p.tokenMatchesName()
	port := p.GatewayPort
	if port <= 0 && !p.GatewayRandomPort {
		port = defaultGatewayPort
	}
	return PublicProfile{
		Hub:         p.Hub,
		Name:        p.Name,
		AllowDirs:   dirs,
		AllowExec:   p.AllowExec,
		AutoConnect: p.AutoConnect,
		SaveToken:   p.SaveToken,
		HasToken:    hasRuntimeToken || hasSealed,
		PairedName:  p.TokenDeviceName,

		Mode:               normalizeMode(p.Mode),
		GatewayPort:        port,
		GatewayRandomPort:  p.GatewayRandomPort,
		GatewayTunnel:      normalizeTunnelProvider(p.GatewayTunnel),
		GatewayNgrokDomain: p.GatewayNgrokDomain,
		GatewayCFPublicURL: p.GatewayCFPublicURL,
		HasNgrokToken:      hasRuntimeNgrok || strings.TrimSpace(p.GatewayNgrokTokenSealed) != "",
		HasCFToken:         hasRuntimeCF || strings.TrimSpace(p.GatewayCFTokenSealed) != "",
		HasGatewayCreds:    hasRuntimeGatewayCreds || (strings.TrimSpace(p.GatewayPasswordSealed) != "" && strings.TrimSpace(p.GatewayBearerSealed) != ""),
	}
}

func DefaultConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("locate user config dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "omni-mcp", "agent-ui.json"), nil
}

func defaultProfile() Profile {
	return Profile{
		AllowDirs:     []string{},
		SaveToken:     true,
		Mode:          ModeRemote,
		GatewayPort:   defaultGatewayPort,
		GatewayTunnel: TunnelCloudflareQuick,
	}
}

func loadProfile(path string) (Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultProfile(), nil
		}
		return Profile{}, fmt.Errorf("read UI profile: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("parse UI profile: %w", err)
	}
	if p.AllowDirs == nil {
		p.AllowDirs = []string{}
	}
	p.Mode = normalizeMode(p.Mode)
	p.GatewayTunnel = normalizeTunnelProvider(p.GatewayTunnel)
	if p.GatewayPort <= 0 && !p.GatewayRandomPort {
		p.GatewayPort = defaultGatewayPort
	}
	return p, nil
}

func saveProfile(path string, p Profile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".agent-ui-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("save config atomically: %w", err)
	}
	return nil
}
