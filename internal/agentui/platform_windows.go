//go:build windows

package agentui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
)

func openBrowser(targetURL string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func pickDirectory(ctx context.Context) (string, error) {
	script := `
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.StartPosition = 'CenterScreen'
$owner.ShowInTaskbar = $false
$owner.Opacity = 0
$owner.Size = New-Object System.Drawing.Size(1,1)
$owner.Show()
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = '选择允许 Omni MCP Agent 访问的工作目录'
$dialog.UseDescriptionForTitle = $true
$dialog.ShowNewFolderButton = $true
$res = $dialog.ShowDialog($owner)
$owner.Close()
$owner.Dispose()
if ($res -eq [System.Windows.Forms.DialogResult]::OK) {
    Write-Output $dialog.SelectedPath
}
`
	units := utf16.Encode([]rune(script))
	var encoded bytes.Buffer
	for _, u := range units {
		encoded.WriteByte(byte(u))
		encoded.WriteByte(byte(u >> 8))
	}
	shell := "powershell.exe"
	if resolved, err := exec.LookPath("powershell.exe"); err == nil {
		shell = resolved
	}
	cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-STA", "-WindowStyle", "Hidden", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded.Bytes()))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("打开文件夹选择器失败: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
