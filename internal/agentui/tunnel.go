package agentui

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	ngrok "golang.ngrok.com/ngrok/v2"
)

const (
	TunnelLocal           = "local"
	TunnelCloudflareQuick = "cloudflare_quick"
	TunnelCloudflareNamed = "cloudflare_named"
	TunnelNgrok           = "ngrok"
)

var tryCloudflareURLRE = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

var (
	errTunnelStartupTimeout = errors.New("等待 Cloudflare Named Tunnel 注册连接超时")
	errTunnelExitedEarly    = errors.New("Cloudflare Named Tunnel 在注册连接前退出")
)

func waitForTunnelReady(ctx context.Context, ready, done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
		return nil
	case <-done:
		return errTunnelExitedEarly
	case <-timer.C:
		return errTunnelStartupTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizeTunnelProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TunnelLocal:
		return TunnelLocal
	case TunnelNgrok:
		return TunnelNgrok
	case TunnelCloudflareNamed:
		return TunnelCloudflareNamed
	case TunnelCloudflareQuick, "":
		return TunnelCloudflareQuick
	default:
		return TunnelCloudflareQuick
	}
}

func managedBinDir() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "omni-mcp", "bin")
}

func cloudflaredBinaryName(goos string) string {
	if goos == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

// findCloudflared locates an installed or previously downloaded cloudflared binary.
func findCloudflared() (string, bool) {
	if p, err := exec.LookPath("cloudflared"); err == nil && p != "" {
		return p, true
	}
	managed := filepath.Join(managedBinDir(), cloudflaredBinaryName(runtime.GOOS))
	if info, err := os.Stat(managed); err == nil && !info.IsDir() {
		return managed, true
	}
	return "", false
}

func cloudflaredDownloadSpec(goos, goarch string) (downloadURL string, isTgz bool, err error) {
	const base = "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	switch goos {
	case "windows":
		// Windows ARM64 runs amd64 binaries natively via emulation.
		return base + "cloudflared-windows-amd64.exe", false, nil
	case "darwin":
		if goarch == "arm64" {
			return base + "cloudflared-darwin-arm64.tgz", true, nil
		}
		return base + "cloudflared-darwin-amd64.tgz", true, nil
	case "linux":
		if goarch == "arm64" {
			return base + "cloudflared-linux-arm64", false, nil
		}
		if goarch == "amd64" {
			return base + "cloudflared-linux-amd64", false, nil
		}
	}
	return "", false, fmt.Errorf("暂不支持在 %s/%s 上自动下载 cloudflared，请手动安装后重试", goos, goarch)
}

func ensureCloudflared(ctx context.Context, onStatus func(state, note string)) (string, error) {
	if p, ok := findCloudflared(); ok {
		return p, nil
	}
	dlURL, isTgz, err := cloudflaredDownloadSpec(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	if onStatus != nil {
		onStatus("downloading", "正在自动下载官方 cloudflared 隧道组件…")
	}

	dir := managedBinDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建组件缓存目录失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", fmt.Errorf("构造 cloudflared 下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "omni-mcp-desktop")
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载 cloudflared 失败（请检查网络或手动安装 cloudflared）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 cloudflared 返回 HTTP %d", resp.StatusCode)
	}

	target := filepath.Join(dir, cloudflaredBinaryName(runtime.GOOS))
	tmp, err := os.CreateTemp(dir, ".cloudflared-*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if isTgz {
		if err := extractCloudflaredFromTgz(resp.Body, tmp); err != nil {
			cleanup()
			return "", fmt.Errorf("解压 cloudflared 安装包失败: %w", err)
		}
	} else {
		if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 160<<20)); err != nil {
			cleanup()
			return "", fmt.Errorf("保存 cloudflared 文件失败: %w", err)
		}
	}
	if err := tmp.Chmod(0o755); err != nil && runtime.GOOS != "windows" {
		cleanup()
		return "", fmt.Errorf("设置 cloudflared 可执行权限失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("写入 cloudflared 组件失败: %w", err)
	}
	return target, nil
}

func extractCloudflaredFromTgz(r io.Reader, dst io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("压缩包中未找到 cloudflared 可执行文件")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) == "cloudflared" {
			_, err := io.Copy(dst, io.LimitReader(tr, 160<<20))
			return err
		}
	}
}

// activeTunnel abstracts an active public tunnel (cloudflared subprocess or embedded ngrok listener).
type activeTunnel interface {
	PublicURL() string
	Listener() net.Listener // non-nil for embedded Go SDK listeners like ngrok
	Done() <-chan struct{}
	Err() error
	Close() error
}

type processTunnel struct {
	publicURL string
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}

	mu  sync.Mutex
	err error
}

func (t *processTunnel) PublicURL() string      { return t.publicURL }
func (t *processTunnel) Listener() net.Listener { return nil }
func (t *processTunnel) Done() <-chan struct{}  { return t.done }
func (t *processTunnel) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *processTunnel) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	if t.done != nil {
		select {
		case <-t.done:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

func startCloudflareQuickTunnel(parentCtx context.Context, localURL string, onStatus func(state, note string)) (activeTunnel, error) {
	bin, err := ensureCloudflared(parentCtx, onStatus)
	if err != nil {
		return nil, err
	}
	if onStatus != nil {
		onStatus("starting", "正在建立 Cloudflare 快速公网隧道…")
	}
	procCtx, cancel := context.WithCancel(parentCtx)
	cmd := exec.CommandContext(procCtx, bin, "tunnel", "--no-autoupdate", "--url", localURL)
	hideCmdWindow(cmd)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		cancel()
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	tun := &processTunnel{
		cmd:    cmd,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	urlCh := make(chan string, 1)
	var tailMu sync.Mutex
	tailLines := make([]string, 0, 8)
	pushTail := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		tailMu.Lock()
		if len(tailLines) >= 8 {
			tailLines = append(tailLines[1:], line)
		} else {
			tailLines = append(tailLines, line)
		}
		tailMu.Unlock()
	}
	getTail := func() string {
		tailMu.Lock()
		defer tailMu.Unlock()
		return strings.Join(tailLines, " | ")
	}

	go func() {
		defer pr.Close()
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
		found := false
		for scanner.Scan() {
			line := scanner.Text()
			pushTail(line)
			if !found {
				if match := tryCloudflareURLRE.FindString(line); match != "" {
					found = true
					select {
					case urlCh <- strings.TrimRight(match, "/"):
					default:
					}
				}
			}
		}
	}()

	go func() {
		waitErr := cmd.Wait()
		_ = pw.Close()
		tun.mu.Lock()
		if waitErr != nil && procCtx.Err() == nil {
			detail := getTail()
			if detail != "" {
				tun.err = fmt.Errorf("cloudflared 异常退出: %v（%s）", waitErr, detail)
			} else {
				tun.err = fmt.Errorf("cloudflared 异常退出: %v", waitErr)
			}
		}
		tun.mu.Unlock()
		close(tun.done)
	}()

	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	select {
	case u := <-urlCh:
		tun.publicURL = u
		return tun, nil
	case <-tun.done:
		if err := tun.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("cloudflared 在分配公网地址前已退出")
	case <-timer.C:
		_ = tun.Close()
		return nil, errors.New("等待 Cloudflare 分配临时 HTTPS 域名超时（35 秒），请检查网络连接")
	case <-parentCtx.Done():
		_ = tun.Close()
		return nil, parentCtx.Err()
	}
}

func startCloudflareNamedTunnel(parentCtx context.Context, publicURL, token string, onStatus func(state, note string)) (activeTunnel, error) {
	cleanURL, err := validatePublicHTTPSURL(publicURL)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("请填写 Cloudflare Named Tunnel Token")
	}
	bin, err := ensureCloudflared(parentCtx, onStatus)
	if err != nil {
		return nil, err
	}
	if onStatus != nil {
		onStatus("starting", "正在连接 Cloudflare Named Tunnel…")
	}

	procCtx, cancel := context.WithCancel(parentCtx)
	cmd := exec.CommandContext(procCtx, bin, "tunnel", "--no-autoupdate", "run")
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+token)
	hideCmdWindow(cmd)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		cancel()
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	tun := &processTunnel{
		publicURL: cleanURL,
		cmd:       cmd,
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	readyCh := make(chan struct{}, 1)
	var tailMu sync.Mutex
	tailLines := make([]string, 0, 8)
	pushTail := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		tailMu.Lock()
		if len(tailLines) >= 8 {
			tailLines = append(tailLines[1:], line)
		} else {
			tailLines = append(tailLines, line)
		}
		tailMu.Unlock()
	}
	getTail := func() string {
		tailMu.Lock()
		defer tailMu.Unlock()
		return strings.Join(tailLines, " | ")
	}

	go func() {
		defer pr.Close()
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
		signaled := false
		for scanner.Scan() {
			line := scanner.Text()
			pushTail(line)
			if !signaled && (strings.Contains(line, "Registered tunnel connection") || strings.Contains(line, "Connection registered")) {
				signaled = true
				select {
				case readyCh <- struct{}{}:
				default:
				}
			}
		}
	}()

	go func() {
		waitErr := cmd.Wait()
		_ = pw.Close()
		tun.mu.Lock()
		if waitErr != nil && procCtx.Err() == nil {
			detail := getTail()
			if detail != "" {
				tun.err = fmt.Errorf("cloudflared Named Tunnel 异常退出: %v（%s）", waitErr, detail)
			} else {
				tun.err = fmt.Errorf("cloudflared Named Tunnel 异常退出: %v", waitErr)
			}
		}
		tun.mu.Unlock()
		close(tun.done)
	}()

	if err := waitForTunnelReady(parentCtx, readyCh, tun.done, 30*time.Second); err != nil {
		detail := getTail()
		tunnelErr := tun.Err()
		_ = tun.Close()
		if errors.Is(err, errTunnelStartupTimeout) && detail != "" {
			return nil, fmt.Errorf("%w（%s）", err, detail)
		}
		if errors.Is(err, errTunnelExitedEarly) && tunnelErr != nil {
			return nil, fmt.Errorf("Cloudflare Named Tunnel 启动失败: %w", tunnelErr)
		}
		return nil, err
	}
	return tun, nil
}

type ngrokActiveTunnel struct {
	agent     ngrok.Agent
	ln        ngrok.EndpointListener
	publicURL string
	done      chan struct{}
}

func (n *ngrokActiveTunnel) PublicURL() string      { return n.publicURL }
func (n *ngrokActiveTunnel) Listener() net.Listener { return n.ln }
func (n *ngrokActiveTunnel) Done() <-chan struct{}  { return n.done }
func (n *ngrokActiveTunnel) Err() error             { return nil }

func (n *ngrokActiveTunnel) Close() error {
	var firstErr error
	if n.ln != nil {
		if err := n.ln.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if n.agent != nil {
		if err := n.agent.Disconnect(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func normalizeNgrokDomain(raw string) (string, error) {
	d := strings.TrimSpace(raw)
	if d == "" {
		return "", nil
	}
	if !strings.Contains(d, "://") {
		d = "https://" + d
	}
	u, err := url.Parse(d)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("ngrok 固定域名格式无效（例如 my-pc.ngrok-free.app）")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("ngrok 固定域名只需填写主机名，不要带路径")
	}
	return strings.ToLower(u.Host), nil
}

func startNgrokTunnel(ctx context.Context, authtoken, domain string, log *slog.Logger, onStatus func(state, note string)) (activeTunnel, error) {
	authtoken = strings.TrimSpace(authtoken)
	if authtoken == "" {
		return nil, errors.New("请先填写 ngrok Authtoken（可在 dashboard.ngrok.com 免费获取）")
	}
	cleanDomain, err := normalizeNgrokDomain(domain)
	if err != nil {
		return nil, err
	}
	if onStatus != nil {
		onStatus("starting", "正在通过内置 ngrok SDK 建立加密公网隧道…")
	}

	ag, err := ngrok.NewAgent(
		ngrok.WithAuthtoken(authtoken),
		ngrok.WithLogger(log),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 ngrok 客户端失败: %w", err)
	}

	opts := []ngrok.EndpointOption{
		ngrok.WithDescription("omni-mcp local gateway"),
	}
	if cleanDomain != "" {
		opts = append(opts, ngrok.WithURL("https://"+cleanDomain))
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 25*time.Second)
	defer dialCancel()
	ln, err := ag.Listen(dialCtx, opts...)
	if err != nil {
		_ = ag.Disconnect()
		return nil, fmt.Errorf("建立 ngrok 隧道失败（请检查 Authtoken 与域名配置）: %w", err)
	}

	pubURL := ""
	if u := ln.URL(); u != nil {
		pubURL = strings.TrimRight(u.String(), "/")
	}
	if pubURL == "" {
		_ = ln.Close()
		_ = ag.Disconnect()
		return nil, errors.New("ngrok 未返回公网地址")
	}

	done := make(chan struct{})
	go func() {
		<-ln.Done()
		close(done)
	}()

	return &ngrokActiveTunnel{
		agent:     ag,
		ln:        ln,
		publicURL: pubURL,
		done:      done,
	}, nil
}

func validatePublicHTTPSURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("公网根地址必须是有效的 https:// 域名（例如 https://mcp.example.com）")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("公网根地址只需填写域名，无需附加 /mcp 路径")
	}
	return u.Scheme + "://" + u.Host, nil
}
