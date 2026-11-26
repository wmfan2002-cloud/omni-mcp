//go:build darwin

package agentui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func desktopProfileDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "omni-mcp", "desktop-profile")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

func findMacAppBrowser() string {
	home, _ := os.UserHomeDir()
	roots := []string{"/Applications"}
	if home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	apps := []struct {
		bundle string
		bin    string
	}{
		{"Google Chrome.app", "Google Chrome"},
		{"Microsoft Edge.app", "Microsoft Edge"},
		{"Brave Browser.app", "Brave Browser"},
		{"Chromium.app", "Chromium"},
		{"Arc.app", "Arc"},
	}
	for _, root := range roots {
		for _, app := range apps {
			p := filepath.Join(root, app.bundle, "Contents", "MacOS", app.bin)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	return ""
}

func openDesktopWindow(targetURL string) error {
	if browser := findMacAppBrowser(); browser != "" {
		args := []string{
			"--app=" + targetURL,
			"--window-size=1180,780",
			"--user-data-dir=" + desktopProfileDir(),
			"--no-first-run",
			"--no-default-browser-check",
		}
		cmd := exec.Command(browser, args...)
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	return openBrowser(targetURL)
}

func openBrowser(targetURL string) error {
	return exec.Command("open", targetURL).Start()
}

func hideCmdWindow(cmd *exec.Cmd) {}

func pickDirectory(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "选择允许 Omni MCP Agent 访问的工作目录")`).Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimRight(strings.TrimSpace(string(out)), "/"), nil
}
