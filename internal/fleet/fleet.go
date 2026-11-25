// Package fleet 抽象"在哪台机器上执行"：本机与 ssh 两种实现，
// 以及建立在它们之上的文件读写。工具层只关心 target，不关心传输方式。
package fleet

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"omni-mcp/internal/config"
	procutil "omni-mcp/internal/process"
)

// LocalTarget 是本机的保留名，指 Omni MCP Hub 所在机器。
const LocalTarget = "local"

// Host 是一台可操作的机器。本机也会以 Host 的形式出现在列表里。
type Host struct {
	Name      string
	Desc      string
	SSH       string
	Workdir   string
	Approval  bool
	Local     bool
	Agent     bool
	Windows   bool
	OS        string
	AllowExec bool
}

// Spec 描述一次执行请求。Target 为空表示本机。
type Spec struct {
	Target         string
	SSH            string
	Command        string
	Workdir        string
	Env            map[string]string
	Stdin          string
	MaxOutputBytes int
	// MergeStderr 把 stderr 并进 stdout，后台任务按时间顺序读输出时需要。
	MergeStderr bool
	// Binary 按原始字节收集输出，不做 UTF-8 对齐；读取任意文件时用。
	Binary bool
}

// Result 是一次前台执行的结果。
type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Duration  time.Duration
	TimedOut  bool
	Truncated bool
}

// Process 是一个已启动、尚未结束的进程。
type Process interface {
	Pid() int
	Out() *CapWriter
	Wait() (int, error)
	Signal(Signal) error
}

type Signal = procutil.Signal

const (
	SignalTERM = procutil.SignalTERM
	SignalKILL = procutil.SignalKILL
	SignalINT  = procutil.SignalINT
	SignalHUP  = procutil.SignalHUP
	SignalQUIT = procutil.SignalQUIT
	SignalUSR1 = procutil.SignalUSR1
	SignalUSR2 = procutil.SignalUSR2
)

// Runner 是本机与 ssh 的公共接口。
type Runner interface {
	Run(ctx context.Context, spec Spec) (Result, error)
	Start(ctx context.Context, spec Spec) (Process, error)
}

// ConnectError 表示连不上目标机器，区别于"命令执行失败"。
type ConnectError struct {
	Host   string
	Detail string
}

func (e *ConnectError) Error() string { return e.Detail }

// Fleet 持有机器清单与两个 Runner，按 target 分发。
type Fleet struct {
	mu            sync.RWMutex
	hosts         []Host
	staticHosts   []Host
	byName        map[string]Host
	agentHosts    []Host
	agent         AgentControl
	agentEnabled  bool
	exposeLocal   bool
	defaultTarget string
	probeTimeout  time.Duration
	approvalCheck string
	local         *Local
	ssh           *SSH
}

type AgentControl interface {
	Call(context.Context, string, string, any, any) error
}

// New 构造机群。cfg 为空时只有 local 一台，Enabled 返回 false。
func New(cfg config.Fleet, shellPath string, shellEnv map[string]string) *Fleet {
	exposeLocal := cfg.LocalExposed()
	defaultTarget := strings.TrimSpace(cfg.DefaultTarget)
	if defaultTarget == "" && exposeLocal {
		defaultTarget = LocalTarget
	}
	f := &Fleet{
		byName:        map[string]Host{},
		exposeLocal:   exposeLocal,
		defaultTarget: defaultTarget,
		probeTimeout:  cfg.ProbeTimeout,
		approvalCheck: cfg.ApprovalCheck,
		local:         NewLocal(shellPath, shellEnv),
		ssh:           NewSSH(cfg.SSHCommand, shellEnv),
	}
	if f.probeTimeout <= 0 {
		f.probeTimeout = 10 * time.Second
	}
	if exposeLocal {
		local := Host{Name: LocalTarget, Desc: "Omni MCP Hub 所在机器", Local: true}
		f.hosts = append(f.hosts, local)
		f.byName[local.Name] = local
	}
	for _, h := range cfg.Hosts {
		host := Host{Name: h.Name, Desc: h.Desc, SSH: h.SSH, Workdir: h.Workdir, Approval: h.Approval}
		if host.SSH == "" {
			host.SSH = host.Name
		}
		f.hosts = append(f.hosts, host)
		f.byName[host.Name] = host
	}
	f.staticHosts = append([]Host(nil), f.hosts...)
	return f
}

// Enabled 报告是否配置了远端机器。
func (f *Fleet) Enabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, h := range f.hosts {
		if !h.Local {
			return true
		}
	}
	return false
}

// ExposeLocal 报告是否暴露本机（local）作为可操作目标。
func (f *Fleet) ExposeLocal() bool { return f.exposeLocal }

// DefaultTarget 返回省略 target 时的默认目标名；为空表示必须显式指定。
func (f *Fleet) DefaultTarget() string { return f.defaultTarget }

// Hosts 返回全部可用机器；若暴露本机，本机在第一位。
func (f *Fleet) Hosts() []Host {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]Host(nil), f.hosts...)
}

// Lookup 按名字找机器；空名字按 DefaultTarget 解析。
func (f *Fleet) Lookup(name string) (Host, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		if f.defaultTarget == "" {
			return Host{}, false
		}
		name = f.defaultTarget
	}
	f.mu.RLock()
	h, ok := f.byName[name]
	f.mu.RUnlock()
	return h, ok
}

func (f *Fleet) SetAgentControl(control AgentControl) {
	f.mu.Lock()
	f.agent = control
	f.mu.Unlock()
}

func (f *Fleet) SetAgentEnabled(enabled bool) {
	f.mu.Lock()
	f.agentEnabled = enabled
	f.mu.Unlock()
}

func (f *Fleet) AgentEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.agentEnabled
}

func (f *Fleet) SetAgentHosts(hosts []Host) {
	f.mu.Lock()
	f.agentHosts = append([]Host(nil), hosts...)
	f.hosts = append(append([]Host(nil), f.staticHosts...), f.agentHosts...)
	f.byName = make(map[string]Host, len(f.hosts))
	for _, host := range f.hosts {
		f.byName[host.Name] = host
	}
	f.mu.Unlock()
}

func (f *Fleet) AgentCall(ctx context.Context, target, method string, params, result any) error {
	f.mu.RLock()
	control := f.agent
	f.mu.RUnlock()
	if control == nil {
		return fmt.Errorf("Agent transport is not enabled")
	}
	return control.Call(ctx, target, method, params, result)
}

// ProbeTimeout 是单台机器的探测超时。
func (f *Fleet) ProbeTimeout() time.Duration { return f.probeTimeout }

// Run 在 target 上执行并等待。
func (f *Fleet) Run(ctx context.Context, spec Spec) (Result, error) {
	h, ok := f.Lookup(spec.Target)
	if !ok {
		return Result{}, fmt.Errorf("未知机器 %q", spec.Target)
	}
	spec.Target = h.Name
	if h.Local {
		return f.local.Run(ctx, spec)
	}
	if h.Agent {
		if !h.AllowExec {
			return Result{}, fmt.Errorf("shell command execution is disabled for Agent %q; restart the Agent with --allow-exec after reviewing its permissions", h.Name)
		}
		var out AgentRunResult
		err := f.AgentCall(ctx, h.Name, "shell.run", AgentRunRequest{
			Command: spec.Command, Workdir: spec.Workdir, Env: spec.Env, Stdin: spec.Stdin,
			MaxOutputBytes: spec.MaxOutputBytes, MergeStderr: spec.MergeStderr, Binary: spec.Binary,
		}, &out)
		if err != nil {
			return Result{}, err
		}
		return Result{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode, Duration: time.Duration(out.DurationNanos), TimedOut: out.TimedOut, Truncated: out.Truncated}, nil
	}
	spec.SSH = h.SSH
	return f.ssh.Run(ctx, spec)
}

// Start 在 target 上启动一个不等待的进程。
func (f *Fleet) Start(ctx context.Context, spec Spec) (Process, error) {
	h, ok := f.Lookup(spec.Target)
	if !ok {
		return nil, fmt.Errorf("未知机器 %q", spec.Target)
	}
	spec.Target = h.Name
	if h.Local {
		return f.local.Start(ctx, spec)
	}
	if h.Agent {
		if !h.AllowExec {
			return nil, fmt.Errorf("shell command execution is disabled for Agent %q; restart the Agent with --allow-exec after reviewing its permissions", h.Name)
		}
		var out AgentStartResult
		if err := f.AgentCall(ctx, h.Name, "shell.start", AgentRunRequest{
			Command: spec.Command, Workdir: spec.Workdir, Env: spec.Env, Stdin: spec.Stdin,
			MaxOutputBytes: spec.MaxOutputBytes, MergeStderr: spec.MergeStderr, Binary: spec.Binary,
		}, &out); err != nil {
			return nil, err
		}
		return &agentProcess{fleet: f, target: h.Name, id: out.ID, out: NewCapWriter(spec.MaxOutputBytes)}, nil
	}
	spec.SSH = h.SSH
	return f.ssh.Start(ctx, spec)
}

type agentProcess struct {
	fleet  *Fleet
	target string
	id     string
	out    *CapWriter
}

func (p *agentProcess) Pid() int        { return 0 }
func (p *agentProcess) Out() *CapWriter { return p.out }
func (p *agentProcess) Wait() (int, error) {
	for {
		var snapshot AgentPollResult
		if err := p.fleet.AgentCall(context.Background(), p.target, "shell.poll", AgentPollRequest{ID: p.id}, &snapshot); err != nil {
			return -1, err
		}
		if snapshot.Output != "" {
			_, _ = p.out.Write([]byte(snapshot.Output))
		}
		if !snapshot.Running {
			return snapshot.ExitCode, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}
func (p *agentProcess) Signal(signal Signal) error {
	return p.fleet.AgentCall(context.Background(), p.target, "shell.signal", AgentSignalRequest{ID: p.id, Signal: signal}, nil)
}

// ApprovalAllowed 执行 approval_check 判断某台需审批的机器当前是否已授权。
// 没配 approval_check 时一律返回 false，调用方据此跳过探测。
func (f *Fleet) ApprovalAllowed(ctx context.Context, host string) bool {
	check := strings.TrimSpace(f.approvalCheck)
	if check == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", strings.ReplaceAll(check, "{host}", Quote(host)))
	return cmd.Run() == nil
}

// ---------- 远程命令拼装 ----------

// Quote 用单引号包住一段文本，供远端 shell 解析。
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuotePath 与 Quote 类似，但保留开头的 ~，让远端 shell 自己展开家目录。
func QuotePath(p string) string {
	switch {
	case p == "~":
		return `"$HOME"`
	case strings.HasPrefix(p, "~/"):
		rest := p[2:]
		if rest == "" {
			return `"$HOME"`
		}
		return `"$HOME"/` + Quote(rest)
	}
	return Quote(p)
}

// BuildRemoteCommand 拼出交给远端登录 shell 的一条命令字符串。
func BuildRemoteCommand(spec Spec) (string, error) {
	var b strings.Builder
	if len(spec.Env) > 0 {
		keys := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("export")
		for _, k := range keys {
			if !validEnvName(k) {
				return "", fmt.Errorf("环境变量名 %q 不合法", k)
			}
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(Quote(spec.Env[k]))
		}
		b.WriteString("; ")
	}
	if spec.Workdir != "" {
		b.WriteString("cd -- ")
		b.WriteString(QuotePath(spec.Workdir))
		b.WriteString(" && ")
	}
	b.WriteString(spec.Command)
	return b.String(), nil
}

func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// mergeEnv 合并进程环境与额外变量，同名时后者覆盖前者。
func mergeEnv(base, extra map[string]string) []string {
	m := make(map[string]string, len(base)+len(extra)+8)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(m))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}
