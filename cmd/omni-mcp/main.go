// omni-mcp 把多台机器的基础操作能力（shell、文件读写、检索）
// 通过 MCP over Streamable HTTP 暴露出去，自带 OAuth 2.1 授权服务器，
// 可直接作为 ChatGPT 的自定义连接器使用。
//
// Copyright (C) 2026 noir017
// 本程序依 GNU Affero General Public License v3 或更新版本授权，详见 LICENSE。
// 因为它是网络服务，AGPL 第 13 条要求：修改后对外提供服务时，必须让使用者
// 能拿到对应源码——服务根页面的源码链接由 server.source_url 控制。
//
// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"omni-mcp/internal/agent"
	"omni-mcp/internal/agentui"
	"omni-mcp/internal/app"
	"omni-mcp/internal/config"
	"omni-mcp/internal/idgen"
	"omni-mcp/internal/policy"
	"omni-mcp/internal/process"
)

const usage = `omni-mcp —— 给 agent 用的统一多机基础工具 MCP 服务

用法：
  omni-mcp serve [-c 配置文件]      启动统一 MCP Hub（默认子命令）
  omni-mcp agent --hub URL --name NAME --allow-dir PATH
                                    将本机反向连接到 Hub
  omni-mcp ui [--listen 127.0.0.1:18485] [--no-open]
                                    启动 Agent 可视化本地控制台（Windows 双击默认进入）
  omni-mcp hash-password [密码]     生成 bcrypt 密码哈希，填到 auth.users
  omni-mcp gen-token                生成一个随机静态令牌，填到 auth.static_tokens
  omni-mcp check-policy '命令'      查看某条 shell 命令会被策略如何处理
  omni-mcp version                  打印版本与许可信息

环境变量（覆盖配置文件）：
  OMNI_CONFIG, OMNI_LISTEN, OMNI_BASE_URL, OMNI_DATA_DIR, OMNI_USER, OMNI_PASSWORD_HASH,
  OMNI_STATIC_TOKEN, OMNI_AUTH_DISABLED, OMNI_ALLOW_ANON_INITIALIZE, OMNI_LOG_LEVEL, OMNI_LOG_FORMAT,
  OMNI_AGENT_TOKEN_<NAME>（每台 Agent 独立配对令牌，例如 office-pc 对应 OFFICE_PC）
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) == 0 && runtime.GOOS == "windows" {
		cmd = "ui"
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return cmdServe(args)
	case "agent":
		return cmdAgent(args)
	case "ui":
		return cmdUI(args)
	case "hash-password":
		return cmdHashPassword(args)
	case "gen-token":
		fmt.Println(idgen.Secret(32))
		return nil
	case "check-policy":
		return cmdCheckPolicy(args)
	case "version":
		fmt.Printf("omni-mcp %s\n", app.Version)
		fmt.Println("Copyright (C) 2026 noir017")
		fmt.Println("License AGPL-3.0-or-later: GNU AGPL version 3 or later <https://gnu.org/licenses/agpl.html>")
		fmt.Println("源码: " + config.DefaultSourceURL)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("未知子命令 %q", cmd)
	}
}

type stringFlags []string

func (values *stringFlags) String() string { return strings.Join(*values, ",") }
func (values *stringFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func cmdAgent(args []string) error {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	hubURL := flags.String("hub", "", "Omni MCP Hub HTTPS origin, e.g. https://mcp.example.com")
	name := flags.String("name", "", "unique lowercase Agent name")
	var allowDirs stringFlags
	flags.Var(&allowDirs, "allow-dir", "project folder to share; repeat for multiple folders")
	allowExec := flags.Bool("allow-exec", false, "allow shell commands as the current local user")
	token := flags.String("token", "", "Hub pairing token; prefer the name-derived environment variable or hidden prompt")
	tokenEnv := flags.String("token-env", "", "environment variable containing the Hub pairing token; defaults from --name")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *hubURL == "" {
		return errors.New("缺少 --hub Hub HTTPS 地址")
	}
	if len(allowDirs) == 0 {
		return errors.New("至少提供一个 --allow-dir 项目目录")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	client, err := agent.NewClient(agent.ClientOptions{
		Hub: *hubURL, Token: *token, TokenEnv: *tokenEnv, Name: *name,
		Version: app.Version, AllowDirs: allowDirs, AllowExec: *allowExec, Log: logger,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), process.Interrupts()...)
	defer stop()
	if err := client.Run(ctx); errors.Is(err, context.Canceled) {
		return nil
	} else {
		return err
	}
}

func cmdUI(args []string) error {
	flags := flag.NewFlagSet("ui", flag.ContinueOnError)
	listen := flags.String("listen", "", "local loopback listen address (default 127.0.0.1:18485)")
	configPath := flags.String("config", "", "UI profile path (default user config directory)")
	noOpen := flags.Bool("no-open", false, "do not automatically open the system browser")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), process.Interrupts()...)
	defer stop()
	return agentui.Run(ctx, agentui.Options{
		Listen:     *listen,
		ConfigPath: *configPath,
		Version:    app.Version,
		NoOpen:     *noOpen,
		Log:        logger,
	})
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "配置文件路径（YAML）；不指定则全部用默认值加环境变量")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		if p := os.Getenv("OMNI_CONFIG"); p != "" {
			*cfgPath = p
		} else if _, err := os.Stat("/etc/omni-mcp/config.yaml"); err == nil {
			*cfgPath = "/etc/omni-mcp/config.yaml"
		}
	}
	return app.Run(*cfgPath)
}

func cmdHashPassword(args []string) error {
	var pw string
	switch {
	case len(args) > 0:
		pw = args[0]
	case !term.IsTerminal(int(os.Stdin.Fd())):
		var line string
		if _, err := fmt.Scanln(&line); err != nil {
			return fmt.Errorf("从标准输入读取密码失败: %w", err)
		}
		pw = line
	default:
		fmt.Fprint(os.Stderr, "请输入密码（不回显）: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "再输入一次: ")
		b2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if string(b) != string(b2) {
			return errors.New("两次输入不一致")
		}
		pw = string(b)
	}
	if len(pw) < 8 {
		return errors.New("密码至少 8 位；这个口令等于服务器 shell 权限，别图省事")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return err
	}
	fmt.Println(string(hash))
	return nil
}

func cmdCheckPolicy(args []string) error {
	fs := flag.NewFlagSet("check-policy", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "配置文件路径，用于加载自定义规则")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("用法: omni-mcp check-policy '要检查的命令'")
	}
	command := strings.Join(rest, " ")

	// 只为加载策略而读配置：这里不需要用户和令牌，临时关掉鉴权校验。
	cfg := config.Default()
	if *cfgPath != "" {
		loaded, err := config.Load(*cfgPath)
		if err != nil {
			return err
		}
		cfg = loaded
	}
	guard, err := policy.NewShellGuard(cfg.Policy, []string{"omni-mcp"})
	if err != nil {
		return err
	}
	v := guard.Inspect(command)
	fmt.Printf("命令: %s\n判定: %s\n", command, v.Action)
	if v.ParseError != "" {
		fmt.Printf("语法解析失败: %s\n", v.ParseError)
	}
	if len(v.Findings) == 0 {
		fmt.Println("\n没有命中任何规则，会直接执行。")
		return nil
	}
	fmt.Println(v.Explain())
	if v.Action == policy.ActionDeny {
		os.Exit(2)
	}
	if v.Action == policy.ActionConfirm {
		os.Exit(3)
	}
	return nil
}
