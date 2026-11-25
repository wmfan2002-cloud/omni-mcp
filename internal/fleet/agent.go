package fleet

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"
)

type AgentRunRequest struct {
	Command        string            `json:"command"`
	Workdir        string            `json:"workdir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Stdin          string            `json:"stdin,omitempty"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
	MergeStderr    bool              `json:"merge_stderr,omitempty"`
	Binary         bool              `json:"binary,omitempty"`
}

type AgentRunResult struct {
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	ExitCode      int    `json:"exit_code"`
	DurationNanos int64  `json:"duration_nanos"`
	TimedOut      bool   `json:"timed_out"`
	Truncated     bool   `json:"truncated"`
}

type AgentStartResult struct {
	ID string `json:"id"`
}

type AgentPollRequest struct {
	ID string `json:"id"`
}

type AgentPollResult struct {
	Output   string `json:"output"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exit_code"`
}

type AgentSignalRequest struct {
	ID     string `json:"id"`
	Signal Signal `json:"signal"`
}

type AgentFilePath struct {
	Path string `json:"path"`
}

type AgentFileWrite struct {
	Path string `json:"path"`
	Data string `json:"data"`
	Mode uint32 `json:"mode,omitempty"`
}

type AgentFileRead struct {
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes"`
}

type AgentFileContent struct {
	Data string `json:"data"`
}

type AgentFileStat struct {
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

type AgentDirList struct {
	Path string `json:"path"`
}

type AgentDirEntry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
	Symlink string    `json:"symlink,omitempty"`
}

type AgentSearchRequest struct {
	Path            string   `json:"path"`
	Pattern         string   `json:"pattern"`
	Glob            string   `json:"glob,omitempty"`
	Literal         bool     `json:"literal,omitempty"`
	CaseInsensitive bool     `json:"case_insensitive,omitempty"`
	ContextLines    int      `json:"context_lines,omitempty"`
	FilesOnly       bool     `json:"files_only,omitempty"`
	Hidden          bool     `json:"hidden,omitempty"`
	MaxResults      int      `json:"max_results"`
	MaxFileBytes    int64    `json:"max_file_bytes"`
	IgnoreDirs      []string `json:"ignore_dirs,omitempty"`
	DenyPaths       []string `json:"deny_paths,omitempty"`
}

type AgentSearchResult struct {
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
	Engine    string   `json:"engine"`
}

type AgentGlobRequest struct {
	Path        string   `json:"path"`
	Pattern     string   `json:"pattern"`
	Hidden      bool     `json:"hidden,omitempty"`
	IncludeDirs bool     `json:"include_dirs,omitempty"`
	MaxResults  int      `json:"max_results"`
	IgnoreDirs  []string `json:"ignore_dirs,omitempty"`
	DenyPaths   []string `json:"deny_paths,omitempty"`
}

type AgentGlobHit struct {
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
	IsDir bool      `json:"is_dir"`
}

type AgentGlobResult struct {
	Files     []AgentGlobHit `json:"files"`
	Truncated bool           `json:"truncated"`
}

type AgentFS struct {
	Control  AgentControl
	Target   string
	MaxBytes int
}

func (r *AgentFS) Stat(ctx context.Context, path string) (FileInfo, error) {
	var info AgentFileStat
	if err := r.Control.Call(ctx, r.Target, "fs.stat", AgentFilePath{Path: path}, &info); err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: info.Size, Mode: os.FileMode(info.Mode), ModTime: info.ModTime, IsDir: info.IsDir}, nil
}

func (r *AgentFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	limit := r.MaxBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	var reply AgentFileContent
	if err := r.Control.Call(ctx, r.Target, "fs.read", AgentFileRead{Path: path, MaxBytes: limit}, &reply); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(reply.Data)
	if err != nil {
		return nil, fmt.Errorf("decode Agent file: %w", err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func (r *AgentFS) ReadHead(ctx context.Context, path string, n int) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	var reply AgentFileContent
	if err := r.Control.Call(ctx, r.Target, "fs.read_head", AgentFileRead{Path: path, MaxBytes: n}, &reply); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(reply.Data)
	if err != nil {
		return nil, fmt.Errorf("decode Agent file header: %w", err)
	}
	if len(data) > n {
		data = data[:n]
	}
	return data, nil
}

func (r *AgentFS) WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	return r.Control.Call(ctx, r.Target, "fs.write", AgentFileWrite{Path: path, Data: base64.StdEncoding.EncodeToString(data), Mode: uint32(mode.Perm())}, nil)
}

func (r *AgentFS) Remove(ctx context.Context, path string) error {
	return r.Control.Call(ctx, r.Target, "fs.remove", AgentFilePath{Path: path}, nil)
}

func (r *AgentFS) MkdirAll(ctx context.Context, path string) error {
	return r.Control.Call(ctx, r.Target, "fs.mkdir", AgentFilePath{Path: path}, nil)
}

func (r *AgentFS) ReadDir(ctx context.Context, path string) ([]AgentDirEntry, error) {
	var entries []AgentDirEntry
	if err := r.Control.Call(ctx, r.Target, "fs.read_dir", AgentDirList{Path: path}, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *AgentFS) Search(ctx context.Context, request AgentSearchRequest) (AgentSearchResult, error) {
	var result AgentSearchResult
	err := r.Control.Call(ctx, r.Target, "fs.search", request, &result)
	return result, err
}

func (r *AgentFS) Glob(ctx context.Context, request AgentGlobRequest) (AgentGlobResult, error) {
	var result AgentGlobResult
	err := r.Control.Call(ctx, r.Target, "fs.glob", request, &result)
	return result, err
}

func (f *Fleet) Call(ctx context.Context, target, method string, params, result any) error {
	return f.AgentCall(ctx, target, method, params, result)
}

var _ FS = (*AgentFS)(nil)
var _ DirectoryFS = (*AgentFS)(nil)

type DirectoryFS interface {
	ReadDir(context.Context, string) ([]AgentDirEntry, error)
}
