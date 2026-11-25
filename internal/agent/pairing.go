package agent

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	pairTTL          = 10 * time.Minute
	maxPendingPairs  = 32
	pairCodeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
)

// PairedDevice represents a Hub-approved Agent device persisted in data_dir/agent_devices.json.
type PairedDevice struct {
	Name       string    `json:"name"`
	TokenHash  string    `json:"token_hash"`
	OS         string    `json:"os,omitempty"`
	ApprovedBy string    `json:"approved_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
}

// DeviceRecord is the safe status view displayed in the Hub /agents portal.
type DeviceRecord struct {
	Name       string     `json:"name"`
	Source     string     `json:"source"` // "hub" | "env"
	Online     bool       `json:"online"`
	OS         string     `json:"os,omitempty"`
	Shell      string     `json:"shell,omitempty"`
	Workdir    string     `json:"workdir,omitempty"`
	AllowExec  bool       `json:"allow_exec"`
	ApprovedBy string     `json:"approved_by,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// PendingPairView is displayed in the Hub /agents approval list.
type PendingPairView struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	OS        string    `json:"os,omitempty"`
	RemoteIP  string    `json:"remote_ip,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pendingPair struct {
	Code        string
	PollToken   string
	Name        string
	OS          string
	RemoteIP    string
	Status      string // pending | approved | rejected
	IssuedToken string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// PairStartResult is returned to an Agent initiating Hub-approved pairing.
type PairStartResult struct {
	Code       string `json:"code"`
	PollToken  string `json:"poll_token"`
	ApproveURL string `json:"approve_url,omitempty"`
	ExpiresIn  int    `json:"expires_in"`
}

// PairPollResult is returned to an Agent polling for approval.
type PairPollResult struct {
	Status string `json:"status"` // pending | approved | rejected | expired
	Name   string `json:"name,omitempty"`
	Token  string `json:"token,omitempty"`
}

// AttachStore enables Hub-managed persistent device pairing inside dataDir.
func (m *Manager) AttachStore(dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create agent device store dir: %w", err)
	}
	storePath := filepath.Join(dataDir, "agent_devices.json")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storePath = storePath
	raw, err := os.ReadFile(storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read agent device store: %w", err)
	}
	var list []PairedDevice
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("parse agent device store: %w", err)
	}
	for _, d := range list {
		if validAgentName.MatchString(d.Name) && d.Name != "local" && d.TokenHash != "" {
			m.paired[d.Name] = d
		}
	}
	return nil
}

func (m *Manager) savePairedLocked() error {
	if m.storePath == "" {
		return nil
	}
	list := make([]PairedDevice, 0, len(m.paired))
	for _, d := range m.paired {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(m.storePath)
	tmp, err := os.CreateTemp(dir, ".agent-devices-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, m.storePath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (m *Manager) prunePendingLocked(now time.Time) {
	for code, p := range m.pending {
		if now.After(p.ExpiresAt) {
			delete(m.pending, code)
		}
	}
}

func generatePairCode() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic("agent: random source failed: " + err.Error())
	}
	out := make([]byte, 9)
	for i := 0; i < 4; i++ {
		out[i] = pairCodeAlphabet[int(buf[i])%len(pairCodeAlphabet)]
	}
	out[4] = '-'
	for i := 0; i < 4; i++ {
		out[5+i] = pairCodeAlphabet[int(buf[4+i])%len(pairCodeAlphabet)]
	}
	return string(out)
}

// StartPairing creates a short-lived pending pairing request for a user-chosen device name.
func (m *Manager) StartPairing(name, goos, remoteIP, baseURL string) (PairStartResult, error) {
	name = strings.TrimSpace(name)
	if !validAgentName.MatchString(name) || name == "local" {
		return PairStartResult{}, fmt.Errorf("设备名称仅支持小写字母、数字和连字符，且不能为 local")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storePath == "" {
		return PairStartResult{}, fmt.Errorf("当前 Hub 未启用动态设备配对存储")
	}
	if m.reserved[name] {
		return PairStartResult{}, fmt.Errorf("设备名称 %q 已被静态 SSH 主机占用，请换一个名称", name)
	}
	now := time.Now()
	m.prunePendingLocked(now)
	if len(m.pending) >= maxPendingPairs {
		return PairStartResult{}, fmt.Errorf("待审批配对请求过多，请稍后重试或先在 Hub 页面处理")
	}
	code := generatePairCode()
	for m.pending[code] != nil {
		code = generatePairCode()
	}
	pollToken := randomID() + randomID()
	m.pending[code] = &pendingPair{
		Code:      code,
		PollToken: pollToken,
		Name:      name,
		OS:        strings.TrimSpace(goos),
		RemoteIP:  remoteIP,
		Status:    "pending",
		CreatedAt: now,
		ExpiresAt: now.Add(pairTTL),
	}
	approveURL := ""
	if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
		approveURL = trimmed + "/agents?code=" + code
	}
	m.log.Info("收到设备配对请求", "name", name, "os", goos, "code", code, "ip", remoteIP)
	return PairStartResult{
		Code:       code,
		PollToken:  pollToken,
		ApproveURL: approveURL,
		ExpiresIn:  int(pairTTL / time.Second),
	}, nil
}

// PollPairing checks the status of a pairing request and delivers the issued token exactly once.
func (m *Manager) PollPairing(code, pollToken string) PairPollResult {
	code = strings.ToUpper(strings.TrimSpace(code))
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.prunePendingLocked(now)
	p, ok := m.pending[code]
	if !ok {
		return PairPollResult{Status: "expired"}
	}
	if len(pollToken) != len(p.PollToken) || subtle.ConstantTimeCompare([]byte(pollToken), []byte(p.PollToken)) != 1 {
		return PairPollResult{Status: "expired"}
	}
	switch p.Status {
	case "approved":
		res := PairPollResult{Status: "approved", Name: p.Name, Token: p.IssuedToken}
		delete(m.pending, code)
		return res
	case "rejected":
		delete(m.pending, code)
		return PairPollResult{Status: "rejected"}
	default:
		return PairPollResult{Status: "pending", Name: p.Name}
	}
}

// ApprovePairing authorizes a pending device, persists its hashed token, and releases the plaintext token once to the polling Agent.
func (m *Manager) ApprovePairing(code, approvedBy string) (PairedDevice, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.prunePendingLocked(now)
	p, ok := m.pending[code]
	if !ok || p.Status != "pending" {
		return PairedDevice{}, fmt.Errorf("配对请求 %q 不存在或已过期", code)
	}
	if m.reserved[p.Name] {
		delete(m.pending, code)
		return PairedDevice{}, fmt.Errorf("设备名 %q 与静态 SSH 主机冲突", p.Name)
	}
	issued := randomID() + randomID()
	dev := PairedDevice{
		Name:       p.Name,
		TokenHash:  hashToken(issued),
		OS:         p.OS,
		ApprovedBy: approvedBy,
		CreatedAt:  now,
	}
	m.paired[p.Name] = dev
	if err := m.savePairedLocked(); err != nil {
		delete(m.paired, p.Name)
		return PairedDevice{}, fmt.Errorf("保存设备授权失败: %w", err)
	}
	p.Status = "approved"
	p.IssuedToken = issued
	m.log.Info("已批准设备配对", "name", p.Name, "code", code, "by", approvedBy)
	return dev, nil
}

// RejectPairing denies a pending pairing request.
func (m *Manager) RejectPairing(code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prunePendingLocked(time.Now())
	p, ok := m.pending[code]
	if !ok || p.Status != "pending" {
		return fmt.Errorf("配对请求 %q 不存在或已过期", code)
	}
	p.Status = "rejected"
	m.log.Info("已拒绝设备配对", "name", p.Name, "code", code)
	return nil
}

// RevokeDevice deletes a Hub-paired device and disconnects it if currently online.
func (m *Manager) RevokeDevice(name string) error {
	name = strings.TrimSpace(name)
	m.mu.Lock()
	if _, ok := m.paired[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("设备 %q 不是通过 Hub 配对的设备（环境变量配置的设备请修改 agent.env）", name)
	}
	delete(m.paired, name)
	err := m.savePairedLocked()
	active := m.hosts[name]
	m.mu.Unlock()
	if active != nil {
		closeHost(active, "device revoked by Hub administrator")
	}
	if err != nil {
		return err
	}
	m.log.Info("已吊销设备授权", "name", name)
	return nil
}

// ListPendingPairings returns currently active pending pairing requests sorted newest-first.
func (m *Manager) ListPendingPairings() []PendingPairView {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prunePendingLocked(time.Now())
	out := make([]PendingPairView, 0, len(m.pending))
	for _, p := range m.pending {
		if p.Status != "pending" {
			continue
		}
		out = append(out, PendingPairView{
			Code:      p.Code,
			Name:      p.Name,
			OS:        p.OS,
			RemoteIP:  p.RemoteIP,
			CreatedAt: p.CreatedAt,
			ExpiresAt: p.ExpiresAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// ListDevices returns all Hub-paired and env-configured devices merged with live connection state.
func (m *Manager) ListDevices() []DeviceRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	byName := map[string]DeviceRecord{}
	for name := range m.tokens {
		byName[name] = DeviceRecord{Name: name, Source: "env"}
	}
	for name, dev := range m.paired {
		created := dev.CreatedAt
		rec := DeviceRecord{
			Name:       name,
			Source:     "hub",
			OS:         dev.OS,
			ApprovedBy: dev.ApprovedBy,
			CreatedAt:  &created,
		}
		if !dev.LastSeenAt.IsZero() {
			seen := dev.LastSeenAt
			rec.LastSeenAt = &seen
		}
		byName[name] = rec
	}
	for name, h := range m.hosts {
		rec := byName[name]
		if rec.Name == "" {
			rec.Name = name
			rec.Source = "env"
		}
		rec.Online = true
		rec.OS = h.info.OS
		rec.Shell = h.info.Shell
		rec.Workdir = h.info.Workdir
		rec.AllowExec = h.info.AllowExec
		byName[name] = rec
	}
	out := make([]DeviceRecord, 0, len(byName))
	for _, rec := range byName {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// HandlePairStart serves POST /agent/pair/start.
func (m *Manager) HandlePairStart(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
			OS   string `json:"os"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writePairJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
			return
		}
		remoteIP := extractRemoteIP(r)
		res, err := m.StartPairing(req.Name, req.OS, remoteIP, baseURL)
		if err != nil {
			writePairJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writePairJSON(w, http.StatusOK, res)
	}
}

// HandlePairPoll serves POST /agent/pair/poll.
func (m *Manager) HandlePairPoll() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Code      string `json:"code"`
			PollToken string `json:"poll_token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writePairJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
			return
		}
		writePairJSON(w, http.StatusOK, m.PollPairing(req.Code, req.PollToken))
	}
}

func writePairJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func extractRemoteIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
