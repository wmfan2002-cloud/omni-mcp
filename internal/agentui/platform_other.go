//go:build !windows && !darwin

package agentui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

func openDesktopWindow(targetURL string) error {
	return openBrowser(targetURL)
}

func openBrowser(targetURL string) error {
	return exec.Command("xdg-open", targetURL).Start()
}

func hideCmdWindow(cmd *exec.Cmd) {}

func pickDirectory(ctx context.Context) (string, error) {
	if zenity, err := exec.LookPath("zenity"); err == nil {
		out, err := exec.CommandContext(ctx, zenity, "--file-selection", "--directory", "--title=选择工作目录").Output()
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(out)), nil
	}
	return "", errors.New("当前系统无图形文件选择器，请直接在输入框粘贴目录路径")
}
