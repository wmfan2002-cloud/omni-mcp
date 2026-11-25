package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"omni-mcp/internal/fleet"
	"omni-mcp/internal/globmatch"
	"omni-mcp/internal/process"
)

const maxProcessOutputBytes = 1 << 20
const maxAgentProcesses = 64

var agentProcessRetention = 5 * time.Minute

var windowsCredentialPaths = []string{
	"**/.ssh/id_*", "**/.aws/credentials", "**/.aws/config", "**/.azure/**",
	"**/.kube/config", "**/.docker/config.json", "**/.config/gh/hosts.yml", "**/.netrc",
	"**/AppData/Roaming/Microsoft/Protect/**", "**/AppData/Local/Microsoft/Credentials/**",
	"**/AppData/Local/Microsoft/Vault/**",
}

type agentProcess struct {
	id     string
	cmd    *exec.Cmd
	out    *fleet.CapWriter
	done   chan struct{}
	mu     sync.Mutex
	code   int
	err    error
	cursor int
	reap   *time.Timer
}

func (c *Client) handle(ctx context.Context, request message) message {
	response := message{Type: "response", ID: request.ID}
	result, err := c.dispatch(ctx, request.Method, request.Params)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	if result != nil {
		response.Result, err = json.Marshal(result)
		if err != nil {
			response.Error = fmt.Sprintf("encode Agent result: %v", err)
		}
	}
	return response
}

func (c *Client) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "shell.run":
		var request fleet.AgentRunRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.runCommand(ctx, request)
	case "shell.start":
		var request fleet.AgentRunRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.startCommand(ctx, request)
	case "shell.poll":
		var request fleet.AgentPollRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.pollProcess(request.ID)
	case "shell.signal":
		var request fleet.AgentSignalRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return nil, c.signalProcess(request)
	case "fs.stat":
		var request fleet.AgentFilePath
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.stat(request.Path)
	case "fs.read", "fs.read_head":
		var request fleet.AgentFileRead
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.readFile(request.Path, request.MaxBytes, method == "fs.read_head")
	case "fs.write":
		var request fleet.AgentFileWrite
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return nil, c.writeFile(request)
	case "fs.remove":
		var request fleet.AgentFilePath
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return nil, c.remove(request.Path)
	case "fs.mkdir":
		var request fleet.AgentFilePath
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return nil, c.mkdir(request.Path)
	case "fs.read_dir":
		var request fleet.AgentDirList
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.readDir(request.Path)
	case "fs.search":
		var request fleet.AgentSearchRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.search(ctx, request)
	case "fs.glob":
		var request fleet.AgentGlobRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		return c.glob(ctx, request)
	case "system.info":
		return c.systemInfo(ctx)
	default:
		return nil, fmt.Errorf("unknown Agent operation %q", method)
	}
}

func (c *Client) resolve(raw string) (*os.Root, string, string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, "", "", fmt.Errorf("path is required")
	}
	normalized := filepath.FromSlash(strings.TrimSpace(raw))
	if !filepath.IsAbs(normalized) {
		normalized = filepath.Join(c.roots[0].Name(), normalized)
	}
	abs, err := filepath.Abs(filepath.Clean(normalized))
	if err != nil {
		return nil, "", "", err
	}
	for _, root := range c.roots {
		relative, err := filepath.Rel(root.Name(), abs)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if relative == "" {
			relative = "."
		}
		if _, err := root.Stat(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, "", "", fmt.Errorf("path is unavailable inside the registered workspace root: %w", err)
		}
		return root, relative, abs, nil
	}
	return nil, "", "", fmt.Errorf("path %q is outside every registered allowed directory", raw)
}

func (c *Client) stat(raw string) (fleet.AgentFileStat, error) {
	root, relative, _, err := c.resolve(raw)
	if err != nil {
		return fleet.AgentFileStat{}, err
	}
	info, err := root.Stat(relative)
	if err != nil {
		return fleet.AgentFileStat{}, err
	}
	return fleet.AgentFileStat{Size: info.Size(), Mode: uint32(info.Mode()), ModTime: info.ModTime(), IsDir: info.IsDir()}, nil
}

func (c *Client) readFile(raw string, maxBytes int, headOnly bool) (fleet.AgentFileContent, error) {
	if maxBytes <= 0 || maxBytes > 32<<20 {
		return fleet.AgentFileContent{}, fmt.Errorf("max_bytes must be between 1 byte and 32 MiB")
	}
	root, relative, _, err := c.resolve(raw)
	if err != nil {
		return fleet.AgentFileContent{}, err
	}
	file, err := root.Open(relative)
	if err != nil {
		return fleet.AgentFileContent{}, err
	}
	defer file.Close()
	var data []byte
	if headOnly {
		data = make([]byte, maxBytes)
		count, err := file.Read(data)
		if err != nil && err != io.EOF {
			return fleet.AgentFileContent{}, err
		}
		data = data[:count]
	} else {
		limited := io.LimitReader(file, int64(maxBytes)+1)
		data, err = io.ReadAll(limited)
		if err != nil {
			return fleet.AgentFileContent{}, err
		}
		if len(data) > maxBytes {
			return fleet.AgentFileContent{}, fmt.Errorf("file exceeds %d bytes", maxBytes)
		}
	}
	return fleet.AgentFileContent{Data: base64.StdEncoding.EncodeToString(data)}, nil
}

func (c *Client) writeFile(request fleet.AgentFileWrite) error {
	if len(request.Data) > (8<<20)*4/3+8 {
		return fmt.Errorf("file exceeds 8 MiB")
	}
	data, err := base64.StdEncoding.DecodeString(request.Data)
	if err != nil {
		return fmt.Errorf("invalid base64 file data: %w", err)
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("file exceeds 8 MiB")
	}
	root, relative, _, err := c.resolve(request.Path)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(request.Mode & 0o777)
	if mode == 0 {
		mode = 0o644
	}
	temporary := filepath.Join(filepath.Dir(relative), ".omni-"+randomID()+".tmp")
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = root.Remove(temporary)
		return writeErr
	}
	if closeErr != nil {
		_ = root.Remove(temporary)
		return closeErr
	}
	if err := root.Rename(temporary, relative); err != nil {
		_ = root.Remove(temporary)
		return err
	}
	return nil
}

func (c *Client) remove(raw string) error {
	root, relative, _, err := c.resolve(raw)
	if err != nil {
		return err
	}
	if relative == "." {
		return fmt.Errorf("an allowed root directory cannot be removed")
	}
	return root.Remove(relative)
}

func (c *Client) mkdir(raw string) error {
	root, relative, _, err := c.resolve(raw)
	if err != nil {
		return err
	}
	return root.MkdirAll(relative, 0o755)
}

func (c *Client) readDir(raw string) ([]fleet.AgentDirEntry, error) {
	root, relative, full, err := c.resolve(raw)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	entries, err := file.ReadDir(-1)
	file.Close()
	if err != nil {
		return nil, err
	}
	out := make([]fleet.AgentDirEntry, 0, len(entries))
	for _, entry := range entries {
		child := filepath.Join(relative, entry.Name())
		info, err := root.Lstat(child)
		if err != nil {
			continue
		}
		var symlink string
		if info.Mode()&os.ModeSymlink != 0 {
			symlink, _ = root.Readlink(child)
		}
		out = append(out, fleet.AgentDirEntry{
			Name: entry.Name(), Size: info.Size(), Mode: uint32(info.Mode()),
			ModTime: info.ModTime(), IsDir: info.IsDir(), Symlink: symlink,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	_ = full
	return out, nil
}

func (c *Client) runCommand(ctx context.Context, request fleet.AgentRunRequest) (fleet.AgentRunResult, error) {
	if !c.opts.AllowExec {
		return fleet.AgentRunResult{}, fmt.Errorf("shell command execution is disabled; restart the Agent with --allow-exec after reviewing its permissions")
	}
	workdir, err := c.commandWorkdir(request.Workdir)
	if err != nil {
		return fleet.AgentRunResult{}, err
	}
	if strings.TrimSpace(request.Command) == "" {
		return fleet.AgentRunResult{}, fmt.Errorf("shell command cannot be empty")
	}
	command := commandArgsForOS(runtime.GOOS, request.Command, exec.LookPath)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workdir
	cmd.Env = commandEnvironment(request.Env)
	if request.Stdin != "" {
		cmd.Stdin = strings.NewReader(request.Stdin)
	}
	process.Configure(cmd)
	cmd.Cancel = func() error { return process.Terminate(cmd) }
	cmd.WaitDelay = 3 * time.Second
	limit := request.MaxOutputBytes
	if limit <= 0 || limit > maxProcessOutputBytes {
		limit = maxProcessOutputBytes
	}
	stdout := fleet.NewCapWriter(limit)
	var stderr io.Writer = fleet.NewCapWriter(limit)
	stderrWriter := stderr.(*fleet.CapWriter)
	if request.MergeStderr {
		stderr = stdout
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err = cmd.Run()
	result := fleet.AgentRunResult{
		Stdout: stdout.String(), Stderr: stderrWriter.String(), DurationNanos: time.Since(start).Nanoseconds(),
		Truncated: stdout.Truncated() || stderrWriter.Truncated(),
		TimedOut:  errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	if err == nil {
		return result, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	if ctx.Err() != nil {
		result.ExitCode = 124
		return result, nil
	}
	return fleet.AgentRunResult{}, err
}

func (c *Client) startCommand(ctx context.Context, request fleet.AgentRunRequest) (fleet.AgentStartResult, error) {
	if !c.opts.AllowExec {
		return fleet.AgentStartResult{}, fmt.Errorf("shell command execution is disabled; restart the Agent with --allow-exec after reviewing its permissions")
	}
	workdir, err := c.commandWorkdir(request.Workdir)
	if err != nil {
		return fleet.AgentStartResult{}, err
	}
	if strings.TrimSpace(request.Command) == "" {
		return fleet.AgentStartResult{}, fmt.Errorf("shell command cannot be empty")
	}
	if err := ctx.Err(); err != nil {
		return fleet.AgentStartResult{}, err
	}
	command := commandArgsForOS(runtime.GOOS, request.Command, exec.LookPath)
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workdir
	cmd.Env = commandEnvironment(request.Env)
	if request.Stdin != "" {
		cmd.Stdin = strings.NewReader(request.Stdin)
	}
	process.Configure(cmd)
	limit := request.MaxOutputBytes
	if limit <= 0 || limit > maxProcessOutputBytes {
		limit = maxProcessOutputBytes
	}
	output := fleet.NewCapWriter(limit)
	cmd.Stdout, cmd.Stderr = output, output
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return fleet.AgentStartResult{}, err
	}
	if len(c.procs) >= maxAgentProcesses {
		c.mu.Unlock()
		return fleet.AgentStartResult{}, fmt.Errorf("Agent already tracks %d background processes; wait for them to finish or reconnect to clear them", maxAgentProcesses)
	}
	if err := cmd.Start(); err != nil {
		c.mu.Unlock()
		return fleet.AgentStartResult{}, err
	}
	id := randomID()
	background := &agentProcess{id: id, cmd: cmd, out: output, done: make(chan struct{})}
	c.procs[id] = background
	c.mu.Unlock()
	go func() {
		err := cmd.Wait()
		code := 0
		if exit := new(exec.ExitError); errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			code = -1
		}
		background.mu.Lock()
		background.code = code
		background.err = err
		close(background.done)
		background.reap = time.AfterFunc(agentProcessRetention, func() { c.removeProcess(background) })
		background.mu.Unlock()
	}()
	return fleet.AgentStartResult{ID: id}, nil
}

func (c *Client) pollProcess(id string) (fleet.AgentPollResult, error) {
	c.mu.Lock()
	proc := c.procs[id]
	c.mu.Unlock()
	if proc == nil {
		return fleet.AgentPollResult{}, fmt.Errorf("unknown process id")
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	output, _, cursor := proc.out.Since(proc.cursor)
	proc.cursor = cursor
	snapshot := fleet.AgentPollResult{Output: output, Running: true, ExitCode: proc.code}
	select {
	case <-proc.done:
		snapshot.Running = false
		snapshot.ExitCode = proc.code
		go c.removeProcess(proc)
	default:
	}
	return snapshot, nil
}

func (c *Client) signalProcess(request fleet.AgentSignalRequest) error {
	c.mu.Lock()
	proc := c.procs[request.ID]
	c.mu.Unlock()
	if proc == nil {
		return fmt.Errorf("unknown process id")
	}
	select {
	case <-proc.done:
		return nil
	default:
	}
	return process.SignalGroup(proc.cmd, request.Signal)
}

func (c *Client) removeProcess(proc *agentProcess) {
	c.mu.Lock()
	if c.procs[proc.id] == proc {
		delete(c.procs, proc.id)
	}
	c.mu.Unlock()
	proc.mu.Lock()
	if proc.reap != nil {
		proc.reap.Stop()
		proc.reap = nil
	}
	proc.mu.Unlock()
}

func (c *Client) cleanupProcesses() {
	c.mu.Lock()
	procs := make([]*agentProcess, 0, len(c.procs))
	for id, proc := range c.procs {
		delete(c.procs, id)
		procs = append(procs, proc)
	}
	c.mu.Unlock()
	for _, proc := range procs {
		proc.mu.Lock()
		if proc.reap != nil {
			proc.reap.Stop()
			proc.reap = nil
		}
		proc.mu.Unlock()
		_ = process.Kill(proc.cmd)
	}
}

type filesystemRoot struct {
	root     *os.Root
	relative string
	full     string
	info     os.FileInfo
}

func (c *Client) files(ctx context.Context, raw string, hidden bool, ignore []string) (filesystemRoot, []filesystemRoot, error) {
	root, relative, full, err := c.resolve(raw)
	if err != nil {
		return filesystemRoot{}, nil, err
	}
	info, err := root.Stat(relative)
	if err != nil {
		return filesystemRoot{}, nil, err
	}
	rootItem := filesystemRoot{root: root, relative: relative, full: full, info: info}
	items := []filesystemRoot{}
	if !info.IsDir() {
		return rootItem, []filesystemRoot{rootItem}, nil
	}
	ignored := make(map[string]bool, len(ignore))
	for _, name := range ignore {
		ignored[strings.ToLower(name)] = true
	}
	var walk func(string, string, int) error
	walk = func(relativeDir, fullDir string, depth int) error {
		if depth > 48 || len(items) >= 50000 {
			return nil
		}
		directory, err := root.Open(relativeDir)
		if err != nil {
			return nil
		}
		entries, err := directory.ReadDir(-1)
		_ = directory.Close()
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if len(items) >= 50000 {
				return nil
			}
			name := entry.Name()
			if !hidden && strings.HasPrefix(name, ".") {
				continue
			}
			childRelative := filepath.Join(relativeDir, name)
			child, err := root.Lstat(childRelative)
			if err != nil {
				continue
			}
			fullPath := filepath.Join(fullDir, name)
			item := filesystemRoot{root: root, relative: childRelative, full: fullPath, info: child}
			items = append(items, item)
			if child.IsDir() && child.Mode()&os.ModeSymlink == 0 && (hidden || !ignored[strings.ToLower(name)]) {
				if err := walk(childRelative, fullPath, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(relative, full, 0); err != nil {
		return rootItem, nil, err
	}
	return rootItem, items, nil
}

func (c *Client) search(ctx context.Context, request fleet.AgentSearchRequest) (fleet.AgentSearchResult, error) {
	if request.Pattern == "" {
		return fleet.AgentSearchResult{}, fmt.Errorf("pattern is required")
	}
	max := request.MaxResults
	if max <= 0 || max > 5000 {
		max = 200
	}
	lineLimit := max * 4
	if lineLimit < 500 {
		lineLimit = 500
	}
	maxFileBytes := request.MaxFileBytes
	if maxFileBytes <= 0 || maxFileBytes > 32<<20 {
		maxFileBytes = 4 << 20
	}
	denyPaths := append(append([]string(nil), request.DenyPaths...), windowsCredentialPaths...)
	pattern := request.Pattern
	if request.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if request.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return fleet.AgentSearchResult{}, fmt.Errorf("invalid search pattern: %w", err)
	}
	root, files, err := c.files(ctx, request.Path, request.Hidden, request.IgnoreDirs)
	if err != nil {
		return fleet.AgentSearchResult{}, err
	}
	lines := make([]string, 0, max+1)
	matchedFiles := make(map[string]bool)
	truncated := false
	for _, file := range files {
		if ctx.Err() != nil {
			return fleet.AgentSearchResult{}, ctx.Err()
		}
		if file.info.IsDir() || file.info.Size() > maxFileBytes || !file.info.Mode().IsRegular() || isDeniedFile(file.full, denyPaths) {
			continue
		}
		relative, err := filepath.Rel(root.full, file.full)
		if err != nil {
			continue
		}
		if request.Glob != "" && !agentSearchGlob(request.Glob, filepath.ToSlash(relative), filepath.Base(relative)) {
			continue
		}
		data, err := file.root.ReadFile(file.relative)
		if err != nil || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		if request.FilesOnly {
			if matcher.Match(data) && !matchedFiles[file.full] {
				matchedFiles[file.full] = true
				lines = append(lines, file.full)
			}
		} else if matches := formatAgentMatches(file.full, string(data), matcher, request.ContextLines, lineLimit-len(lines)); len(matches) > 0 {
			lines = append(lines, matches...)
		}
		if len(lines) > max {
			lines = lines[:max]
			truncated = true
			break
		}
	}
	if len(lines) > max {
		lines = lines[:max]
		truncated = true
	}
	return fleet.AgentSearchResult{Lines: lines, Truncated: truncated, Engine: "Omni MCP Agent"}, nil
}

func formatAgentMatches(path, contents string, matcher *regexp.Regexp, contextLines, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if contextLines < 0 || contextLines > 20 {
		contextLines = 0
	}
	lines := strings.Split(contents, "\n")
	if contextLines == 0 {
		out := []string{}
		for index, line := range lines {
			line = strings.TrimSuffix(line, "\r")
			if matcher.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d:%s", path, index+1, line))
				if len(out) >= limit {
					break
				}
			}
		}
		return out
	}
	matched := make([]bool, len(lines))
	for index, line := range lines {
		if matcher.MatchString(strings.TrimSuffix(line, "\r")) {
			start, end := index-contextLines, index+contextLines
			if start < 0 {
				start = 0
			}
			if end >= len(lines) {
				end = len(lines) - 1
			}
			for cursor := start; cursor <= end; cursor++ {
				matched[cursor] = true
			}
		}
	}
	out := []string{}
	previous := -2
	for index, include := range matched {
		if !include {
			continue
		}
		if previous != index-1 && len(out) > 0 {
			out = append(out, "--")
		}
		separator := "-"
		if matcher.MatchString(strings.TrimSuffix(lines[index], "\r")) {
			separator = ":"
		}
		out = append(out, fmt.Sprintf("%s%s%d%s%s", path, separator, index+1, separator, strings.TrimSuffix(lines[index], "\r")))
		previous = index
		if len(out) >= limit {
			break
		}
	}
	return out
}

func agentSearchGlob(pattern, relative, name string) bool {
	pattern = strings.ToLower(filepath.ToSlash(pattern))
	relative = strings.ToLower(filepath.ToSlash(relative))
	name = strings.ToLower(name)
	negate := strings.HasPrefix(pattern, "!")
	pattern = strings.TrimPrefix(pattern, "!")
	hit := globmatch.Match(pattern, relative) || globmatch.Match(pattern, name)
	if !hit && !strings.Contains(pattern, "/") {
		hit = globmatch.Match("**/"+pattern, relative)
	}
	if negate {
		return !hit
	}
	return hit
}

func isDeniedFile(path string, patterns []string) bool {
	path = strings.ToLower(filepath.ToSlash(path))
	for _, pattern := range patterns {
		if globmatch.Match(strings.ToLower(filepath.ToSlash(pattern)), path) {
			return true
		}
	}
	return false
}

func (c *Client) glob(ctx context.Context, request fleet.AgentGlobRequest) (fleet.AgentGlobResult, error) {
	max := request.MaxResults
	if max <= 0 || max > 50000 {
		max = 5000
	}
	root, files, err := c.files(ctx, request.Path, request.Hidden, request.IgnoreDirs)
	if err != nil {
		return fleet.AgentGlobResult{}, err
	}
	denyPaths := append(append([]string(nil), request.DenyPaths...), windowsCredentialPaths...)
	out := fleet.AgentGlobResult{Files: make([]fleet.AgentGlobHit, 0, min(max, 512))}
	for _, file := range files {
		if ctx.Err() != nil {
			return fleet.AgentGlobResult{}, ctx.Err()
		}
		if isDeniedFile(file.full, denyPaths) {
			continue
		}
		if file.info.IsDir() && (!request.IncludeDirs || file.full == root.full) {
			continue
		}
		relative, err := filepath.Rel(root.full, file.full)
		if err != nil {
			continue
		}
		subject := filepath.ToSlash(relative)
		pattern := strings.ToLower(filepath.ToSlash(request.Pattern))
		subject = strings.ToLower(subject)
		if !strings.Contains(request.Pattern, "/") {
			if !globmatch.Match(pattern, strings.ToLower(filepath.Base(file.full))) && !globmatch.Match("**/"+pattern, subject) {
				continue
			}
		} else if !globmatch.Match(pattern, subject) {
			continue
		}
		out.Files = append(out.Files, fleet.AgentGlobHit{
			Path: file.full, Size: file.info.Size(), Mtime: file.info.ModTime(), IsDir: file.info.IsDir(),
		})
		if len(out.Files) > max {
			out.Files = out.Files[:max]
			out.Truncated = true
			break
		}
	}
	return out, nil
}

type systemInfo struct {
	Name      string `json:"name"`
	OS        string `json:"os"`
	Shell     string `json:"shell"`
	Version   string `json:"version"`
	Workdir   string `json:"workdir"`
	AllowExec bool   `json:"allow_exec"`
}

func (c *Client) systemInfo(ctx context.Context) (systemInfo, error) {
	return systemInfo{Name: c.opts.Name, OS: runtime.GOOS, Shell: agentShellName(runtime.GOOS), Version: c.opts.Version, Workdir: c.roots[0].Name(), AllowExec: c.opts.AllowExec}, nil
}

func (c *Client) commandWorkdir(raw string) (string, error) {
	if raw == "" {
		return c.roots[0].Name(), nil
	}
	_, _, full, err := c.resolve(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("shell working directory is not a directory")
	}
	return full, nil
}

func agentShellName(goos string) string {
	if goos == "windows" {
		return "PowerShell (pwsh.exe preferred; powershell.exe fallback)"
	}
	return "bash -c (sh -c fallback)"
}

func commandArgsForOS(goos, script string, lookPath func(string) (string, error)) []string {
	if goos != "windows" {
		shell := "sh"
		if resolved, err := lookPath("bash"); err == nil {
			shell = resolved
		} else if resolved, err := lookPath("sh"); err == nil {
			shell = resolved
		}
		return []string{shell, "-c", script}
	}
	shell := "powershell.exe"
	for _, candidate := range []string{"pwsh.exe", "pwsh", "powershell.exe"} {
		if resolved, err := lookPath(candidate); err == nil {
			shell = resolved
			break
		}
	}
	return powershellArgs(shell, script)
}

func powershellArgs(executable, script string) []string {
	setup := "$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new(); $OutputEncoding=[System.Text.UTF8Encoding]::new(); " + script
	units := utf16.Encode([]rune(setup))
	var encoded bytes.Buffer
	for _, unit := range units {
		encoded.WriteByte(byte(unit))
		encoded.WriteByte(byte(unit >> 8))
	}
	return []string{executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded.Bytes())}
}

func commandEnvironment(extra map[string]string) []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true,
		"TEMP": true, "TMP": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"APPDATA": true, "LOCALAPPDATA": true, "PROGRAMFILES": true, "PROGRAMFILES(X86)": true,
		"PROCESSOR_ARCHITECTURE": true, "NUMBER_OF_PROCESSORS": true,
		"HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "LANG": true,
		"LC_ALL": true, "LC_CTYPE": true, "TERM": true, "XDG_CONFIG_HOME": true,
	}
	environment := map[string]string{}
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if found && allowed[upper] && !strings.HasPrefix(upper, "OMNI_") {
			environment[upper] = value
		}
	}
	for key, value := range extra {
		upper := strings.ToUpper(key)
		if !envName.MatchString(key) || strings.ContainsAny(value, "\x00") || sensitiveEnv.MatchString(upper) {
			continue
		}
		environment[upper] = value
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(environment))
	for _, key := range keys {
		out = append(out, key+"="+environment[key])
	}
	return out
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var sensitiveEnv = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|CREDENTIAL|API.?KEY|AUTH)`)
