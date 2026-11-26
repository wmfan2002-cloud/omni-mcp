//go:build !windows && !darwin

package agentui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const fileTokenPrefix = "b64:"

func tokenStorageDescription() string {
	return "本地 0600 配置文件保存"
}

func sealToken(plain string) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", nil
	}
	return fileTokenPrefix + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}

func unsealToken(sealed string) (string, error) {
	sealed = strings.TrimSpace(sealed)
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, fileTokenPrefix) {
		return "", errors.New("unsupported stored token format")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, fileTokenPrefix))
	if err != nil {
		return "", fmt.Errorf("decode stored token: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func deleteToken(string) error { return nil }
