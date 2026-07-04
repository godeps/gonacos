// Package cluster implements node membership, plugins, and operational APIs.
//
// In standalone mode the cluster contains a single member (self). The service
// exposes member listing, plugin management, and operational endpoints that
// match the Nacos v3 admin/core contract. Cluster replication and raft groups
// are out of scope for standalone mode and are documented in the design as a
// follow-up phase.
package cluster

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Mode is the deployment mode of the server.
type Mode string

const (
	ModeStandalone Mode = "standalone"
	ModeCluster    Mode = "cluster"
	ModeRedis      Mode = "redis"
)

const (
	// DefaultAPIPort is the default HTTP API port.
	DefaultAPIPort = 8848
	// DefaultGRPCPort is the default gRPC port.
	DefaultGRPCPort = 9848
	// DefaultRaftPort is the default raft port.
	DefaultRaftPort = 9849
)

var (
	ErrMissingMemberID = errors.New("member id is required")
	ErrMemberNotFound  = errors.New("member not found")
	ErrMissingPluginID = errors.New("plugin id is required")
	ErrPluginNotFound  = errors.New("plugin not found")
	ErrNotClusterMode  = errors.New("operation requires cluster mode")
)

// Member is a cluster node. In standalone mode the cluster has exactly one
// member marked as self.
type Member struct {
	ID         string            `json:"id"`
	IP         string            `json:"ip"`
	Port       int               `json:"port"`
	State      string            `json:"state"`
	APIPort    int               `json:"apiPort,omitempty"`
	GRPCPort   int               `json:"grpcPort,omitempty"`
	RaftPort   int               `json:"raftPort,omitempty"`
	GrpcAPIInfo string           `json:"grpcApiInfo,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Abilities  []string          `json:"abilities,omitempty"`
	IsSelf     bool              `json:"isSelf,omitempty"`
	UpdatedAt  time.Time         `json:"updatedAt,omitempty"`
}

// Plugin is a registered server plugin.
type Plugin struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Available   bool              `json:"available"`
	Enabled     bool              `json:"enabled"`
	Config      map[string]string `json:"config,omitempty"`
	Status      map[string]string `json:"status,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt,omitempty"`
}

// Service owns the in-memory cluster state.
type Service struct {
	mu      sync.RWMutex
	mode    Mode
	self    *Member
	members map[string]*Member
	plugins map[string]*Plugin
	logLevel string
}

// NewService creates a cluster service in the given mode. In standalone mode
// the cluster is seeded with a single self member.
func NewService(mode Mode, selfIP string, apiPort, grpcPort, raftPort int) *Service {
	if apiPort == 0 {
		apiPort = DefaultAPIPort
	}
	if grpcPort == 0 {
		grpcPort = DefaultGRPCPort
	}
	if raftPort == 0 {
		raftPort = DefaultRaftPort
	}
	if selfIP == "" {
		selfIP = "127.0.0.1"
	}
	now := time.Now()
	self := &Member{
		ID:        deriveMemberID(selfIP, apiPort),
		IP:        selfIP,
		Port:      apiPort,
		State:     "UP",
		APIPort:   apiPort,
		GRPCPort:  grpcPort,
		RaftPort:  raftPort,
		Abilities: []string{"config", "naming", "auth", "ai"},
		IsSelf:    true,
		UpdatedAt: now,
	}
	svc := &Service{
		mode:    mode,
		self:    self,
		members: map[string]*Member{self.ID: self},
		plugins: seedPlugins(),
		logLevel: "INFO",
	}
	return svc
}

// Mode returns the deployment mode.
func (s *Service) Mode() Mode { return s.mode }

// Self returns the current node member.
func (s *Service) Self() *Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.self
}

// ListMembers returns all cluster members.
func (s *Service) ListMembers() []*Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Member, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m)
	}
	return out
}

// GetMember returns the member by ID.
func (s *Service) GetMember(id string) (*Member, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingMemberID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[id]
	if !ok {
		return nil, ErrMemberNotFound
	}
	return m, nil
}

// AddMember registers a new cluster member. Returns ErrNotClusterMode in
// standalone mode.
func (s *Service) AddMember(m Member) (*Member, error) {
	if s.mode == ModeStandalone {
		return nil, ErrNotClusterMode
	}
	m.ID = strings.TrimSpace(m.ID)
	if m.ID == "" {
		return nil, ErrMissingMemberID
	}
	m.UpdatedAt = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := m
	s.members[m.ID] = &cp
	return &cp, nil
}

// RemoveMember removes a cluster member. The self member cannot be removed.
func (s *Service) RemoveMember(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrMissingMemberID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.self.ID {
		return errors.New("cannot remove self")
	}
	if _, ok := s.members[id]; !ok {
		return ErrMemberNotFound
	}
	delete(s.members, id)
	return nil
}

// UpdateLookup updates the lookup type for the cluster. In standalone mode this
// is a no-op that returns the current member list.
func (s *Service) UpdateLookup(lookupType string) ([]*Member, error) {
	_ = lookupType
	return s.ListMembers(), nil
}

// UpdateNodes sets the cluster member list. In standalone mode this is a no-op
// that returns the current member list.
func (s *Service) UpdateNodes(members []Member) ([]*Member, error) {
	if s.mode == ModeStandalone {
		return s.ListMembers(), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members = map[string]*Member{}
	for _, m := range members {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			continue
		}
		if m.ID == s.self.ID {
			s.members[m.ID] = s.self
			continue
		}
		cp := m
		cp.UpdatedAt = time.Now()
		s.members[m.ID] = &cp
	}
	return s.ListMembers(), nil
}

// ListPlugins returns all registered plugins.
func (s *Service) ListPlugins() []*Plugin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Plugin, 0, len(s.plugins))
	for _, p := range s.plugins {
		out = append(out, p)
	}
	return out
}

// GetPlugin returns the plugin by ID.
func (s *Service) GetPlugin(id string) (*Plugin, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingPluginID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plugins[id]
	if !ok {
		return nil, ErrPluginNotFound
	}
	return p, nil
}

// UpdatePluginStatus toggles a plugin's enabled state.
func (s *Service) UpdatePluginStatus(id string, enabled bool) (*Plugin, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingPluginID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plugins[id]
	if !ok {
		return nil, ErrPluginNotFound
	}
	p.Enabled = enabled
	p.UpdatedAt = time.Now()
	return p, nil
}

// UpdatePluginConfig merges config into a plugin.
func (s *Service) UpdatePluginConfig(id string, config map[string]string) (*Plugin, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingPluginID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plugins[id]
	if !ok {
		return nil, ErrPluginNotFound
	}
	if p.Config == nil {
		p.Config = map[string]string{}
	}
	for k, v := range config {
		p.Config[k] = v
	}
	p.UpdatedAt = time.Now()
	return p, nil
}

// LogLevel returns the current server log level.
func (s *Service) LogLevel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.logLevel
}

// SetLogLevel updates the server log level.
func (s *Service) SetLogLevel(level string) {
	level = strings.TrimSpace(level)
	if level == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logLevel = strings.ToUpper(level)
}

// IDs returns the snowflake ID state. In standalone mode this returns a
// stubbed map since there is no distributed ID generator.
func (s *Service) IDs() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"serverId":  s.self.ID,
		"workerId":  0,
		"datacenter": "",
		"mode":      string(s.mode),
	}
}

// RaftOps performs a raft operation. In standalone mode raft is not active,
// so this returns a not-available stub.
func (s *Service) RaftOps(command, groupId string) (map[string]any, error) {
	if s.mode == ModeStandalone {
		return map[string]any{
			"available": false,
			"mode":      string(s.mode),
			"reason":    "standalone mode has no raft group",
		}, nil
	}
	return map[string]any{
		"available": true,
		"command":   command,
		"groupId":   groupId,
	}, nil
}

// LoaderMetrics returns reload metrics for the cluster. In standalone mode this
// returns a single-node snapshot.
func (s *Service) LoaderMetrics() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"mode":    string(s.mode),
		"members": s.ListMembers(),
		"reloadCount": 0,
	}
}

// CurrentClients returns the clients connected to this node. Without a
// persistent connection registry this returns a count-only snapshot.
func (s *Service) CurrentClients() map[string]any {
	return map[string]any{
		"count":  0,
		"clients": []any{},
	}
}

// SmartReload triggers a reload across the cluster. In standalone mode this is
// a single-node no-op that returns the reload count.
func (s *Service) SmartReload() map[string]any {
	return map[string]any{
		"reloaded": 0,
		"mode":     string(s.mode),
	}
}

// ReloadSingle reloads a single client. Without a connection registry this is
// a no-op.
func (s *Service) ReloadSingle(clientID string) map[string]any {
	return map[string]any{
		"clientId": clientID,
		"reloaded": false,
		"reason":   "no connection registry in standalone mode",
	}
}

// ReloadCount returns the total reload count.
func (s *Service) ReloadCount() map[string]any {
	return map[string]any{
		"count": 0,
		"mode":  string(s.mode),
	}
}

// deriveMemberID produces a stable ID for a node from its IP and port.
func deriveMemberID(ip string, port int) string {
	return ip + ":" + itoa(port)
}

// DeriveMemberID is the exported version of deriveMemberID. It accepts a
// string port for convenience when working with net.SplitHostPort output.
func DeriveMemberID(ip, port string) string {
	p := 0
	for _, c := range port {
		if c >= '0' && c <= '9' {
			p = p*10 + int(c-'0')
		}
	}
	if p == 0 {
		p = DefaultAPIPort
	}
	return deriveMemberID(ip, p)
}

// SetMode updates the cluster mode at runtime. Used to switch to Redis mode
// after the Redis sync layer is initialized.
func (s *Service) SetMode(mode Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func seedPlugins() map[string]*Plugin {
	now := time.Now()
	plugins := []*Plugin{
		{ID: "nacos-default", Name: "DefaultPlugin", Description: "Built-in default plugin", Type: "builtin", Available: true, Enabled: true, UpdatedAt: now},
		{ID: "nacos-auth", Name: "AuthPlugin", Description: "Built-in authentication plugin", Type: "builtin", Available: true, Enabled: true, UpdatedAt: now},
		{ID: "nacos-ipc", Name: "IpcPlugin", Description: "Built-in inter-process communication plugin", Type: "builtin", Available: true, Enabled: true, UpdatedAt: now},
	}
	out := map[string]*Plugin{}
	for _, p := range plugins {
		cp := p
		out[p.ID] = cp
	}
	return out
}
