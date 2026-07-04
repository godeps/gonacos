package ai

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// McpServer is the Nacos-compatible MCP server representation. Concurrency
// safety is provided by the owning mcpStore mutex; all access goes through
// the store.
type McpServer struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Protocol    string            `json:"protocol"`
	Endpoint    string            `json:"endpoint"`
	Tools       []McpTool         `json:"tools,omitempty"`
	Labels      []string          `json:"labels,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// McpTool is a tool exposed by an MCP server.
type McpTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	InputSchema map[string]any    `json:"inputSchema,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// mcpStore owns the in-memory MCP server registry.
type mcpStore struct {
	mu      sync.RWMutex
	servers map[string]*McpServer
}

func newMcpStore() *mcpStore {
	return &mcpStore{servers: map[string]*McpServer{}}
}

func (s *mcpStore) get(id string) (*McpServer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv, ok := s.servers[id]
	return srv, ok
}

func (s *mcpStore) list() []*McpServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*McpServer, 0, len(s.servers))
	for _, srv := range s.servers {
		out = append(out, srv)
	}
	return out
}

func (s *mcpStore) put(srv *McpServer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers[srv.ID] = srv
}

func (s *mcpStore) delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[id]; !ok {
		return false
	}
	delete(s.servers, id)
	return true
}

// CreateMcpServer registers a new MCP server.
func (s *Service) CreateMcpServer(srv McpServer) (*McpServer, error) {
	srv.ID = strings.TrimSpace(srv.ID)
	srv.Name = strings.TrimSpace(srv.Name)
	if srv.ID == "" {
		return nil, ErrMissingID
	}
	if srv.Name == "" {
		return nil, ErrMissingName
	}
	if srv.Protocol == "" {
		srv.Protocol = "http"
	}
	now := time.Now()
	srv.CreatedAt = now
	srv.UpdatedAt = now
	s.mcp.put(&srv)
	return &srv, nil
}

// UpdateMcpServer mutates an existing MCP server.
func (s *Service) UpdateMcpServer(srv McpServer) (*McpServer, error) {
	srv.ID = strings.TrimSpace(srv.ID)
	if srv.ID == "" {
		return nil, ErrMissingID
	}
	s.mcp.mu.Lock()
	defer s.mcp.mu.Unlock()
	existing, ok := s.mcp.servers[srv.ID]
	if !ok {
		return nil, ErrResourceNotFound
	}
	if srv.Name != "" {
		existing.Name = srv.Name
	}
	if srv.Description != "" {
		existing.Description = srv.Description
	}
	if srv.Protocol != "" {
		existing.Protocol = srv.Protocol
	}
	if srv.Endpoint != "" {
		existing.Endpoint = srv.Endpoint
	}
	if srv.Tools != nil {
		existing.Tools = append([]McpTool(nil), srv.Tools...)
	}
	if srv.Labels != nil {
		existing.Labels = cloneStrings(srv.Labels)
	}
	if srv.Metadata != nil {
		existing.Metadata = cloneMetadata(srv.Metadata)
	}
	existing.UpdatedAt = time.Now()
	return existing, nil
}

// GetMcpServer returns the MCP server by ID.
func (s *Service) GetMcpServer(id string) (*McpServer, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	srv, ok := s.mcp.get(id)
	if !ok {
		return nil, ErrResourceNotFound
	}
	return srv, nil
}

// ListMcpServers returns all MCP servers.
func (s *Service) ListMcpServers() []*McpServer { return s.mcp.list() }

// DeleteMcpServer removes an MCP server.
func (s *Service) DeleteMcpServer(id string) error {
	if !s.mcp.delete(id) {
		return ErrResourceNotFound
	}
	return nil
}

// ImportToolsFromMcp fetches tools from a remote MCP server. Without a live
// remote MCP client this returns the locally-registered tools for the server.
func (s *Service) ImportToolsFromMcp(id string) ([]McpTool, error) {
	srv, err := s.GetMcpServer(id)
	if err != nil {
		return nil, err
	}
	out := make([]McpTool, len(srv.Tools))
	copy(out, srv.Tools)
	return out, nil
}

// ValidateMcpImport checks that an MCP server URL and credentials are usable.
// Without a remote client, this returns nil for any non-empty URL.
func (s *Service) ValidateMcpImport(url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("url is required")
	}
	return nil
}

// ExecuteMcpImport imports tools from a remote MCP server. Without a remote
// client, this is a no-op and returns the existing tools.
func (s *Service) ExecuteMcpImport(id string) ([]McpTool, error) {
	return s.ImportToolsFromMcp(id)
}
