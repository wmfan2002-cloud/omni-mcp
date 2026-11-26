//go:build darwin

package agentui

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	keychainPrefix  = "kc:"
	fileTokenPrefix = "b64:"
	keychainService = "omni-mcp-agent-ui"
)

func tokenStorageDescription() string {
	return "macOS Keychain"
}

func randomKeychainAccount() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "token-" + hex.EncodeToString(buf), nil
}

func sealToken(plain string) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", nil
	}
	acct, err := randomKeychainAccount()
	if err == nil && !strings.ContainsAny(plain, "\r\n\"\\") {
		cmd := exec.Command("/usr/bin/security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %q -a %q -w %q\n", keychainService, acct, plain))
		if out, kerr := cmd.CombinedOutput(); kerr == nil && !strings.Contains(strings.ToLower(string(out)), "error") {
			return keychainPrefix + acct, nil
		}
	}
	// Headless CI / locked keychain fallback (stored in 0600 profile file)
	return fileTokenPrefix + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}

func unsealToken(sealed string) (string, error) {
	sealed = strings.TrimSpace(sealed)
	if sealed == "" {
		return "", nil
	}
	if strings.HasPrefix(sealed, keychainPrefix) {
		acct := strings.TrimSpace(strings.TrimPrefix(sealed, keychainPrefix))
		if acct == "" {
			return "", errors.New("empty macOS Keychain token reference")
		}
		out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", keychainService, "-a", acct, "-w").Output()
		if err != nil {
			return "", fmt.Errorf("read macOS Keychain token: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if strings.HasPrefix(sealed, fileTokenPrefix) {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, fileTokenPrefix))
		if err != nil {
			return "", fmt.Errorf("decode stored token: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return "", errors.New("unsupported stored token format on macOS")
}
