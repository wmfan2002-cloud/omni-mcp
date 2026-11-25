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
}

func (p Profile) tokenMatchesName() bool {
	return strings.TrimSpace(p.TokenDeviceName) == "" || p.TokenDeviceName == p.Name
}

func (p Profile) Public(hasRuntimeToken bool) PublicProfile {
	dirs := append([]string(nil), p.AllowDirs...)
	if dirs == nil {
		dirs = []string{}
	}
	hasSealed := strings.TrimSpace(p.TokenSealed) != "" && p.tokenMatchesName()
	return PublicProfile{
		Hub:         p.Hub,
		Name:        p.Name,
		AllowDirs:   dirs,
		AllowExec:   p.AllowExec,
		AutoConnect: p.AutoConnect,
		SaveToken:   p.SaveToken,
		HasToken:    hasRuntimeToken || hasSealed,
		PairedName:  p.TokenDeviceName,
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
		AllowDirs: []string{},
		SaveToken: true,
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
