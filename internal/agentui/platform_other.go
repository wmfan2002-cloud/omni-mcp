//go:build !windows

package agentui

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

func openBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

func pickDirectory(ctx context.Context) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "选择允许 Omni MCP Agent 访问的工作目录")`).Output()
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(out)), nil
	default:
		if zenity, err := exec.LookPath("zenity"); err == nil {
			out, err := exec.CommandContext(ctx, zenity, "--file-selection", "--directory", "--title=选择工作目录").Output()
			if err != nil {
				return "", nil
			}
			return strings.TrimSpace(string(out)), nil
		}
		return "", errors.New("当前系统无图形文件选择器，请直接在输入框粘贴目录路径")
	}
}
