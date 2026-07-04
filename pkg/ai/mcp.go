package ai

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/ai/mcpclient"
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

// ImportToolsFromMcp fetches tools from a remote MCP server. If the server has
// an endpoint configured, this dials the server via streamable HTTP and lists
// its tools. If the dial fails or the endpoint is empty, this falls back to
// returning the locally-registered tools.
func (s *Service) ImportToolsFromMcp(id string) ([]McpTool, error) {
	srv, err := s.GetMcpServer(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(srv.Endpoint) == "" {
		out := make([]McpTool, len(srv.Tools))
		copy(out, srv.Tools)
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, srv.Endpoint, mcpclient.DialOptions{
		Headers: s.mcpHeaders(srv),
	})
	if err != nil {
		// Fall back to local tools on dial failure.
		out := make([]McpTool, len(srv.Tools))
		copy(out, srv.Tools)
		return out, nil
	}
	defer client.Close()
	remote, err := client.ListTools(ctx)
	if err != nil {
		out := make([]McpTool, len(srv.Tools))
		copy(out, srv.Tools)
		return out, nil
	}
	out := make([]McpTool, 0, len(remote))
	for _, t := range remote {
		tool := McpTool{
			Name:        t.Name,
			Description: t.Description,
		}
		if schema, ok := t.InputSchema.(map[string]any); ok {
			tool.InputSchema = schema
		}
		out = append(out, tool)
	}
	return out, nil
}

// ValidateMcpImport dials the remote MCP server and runs an Initialize round-trip.
// Returns nil if the server is reachable and the protocol handshake succeeds.
func (s *Service) ValidateMcpImport(url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("url is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mcpclient.Dial(ctx, url, mcpclient.DialOptions{})
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.ListTools(ctx)
	return err
}

// ExecuteMcpImport imports tools from a remote MCP server and persists them
// on the local McpServer entry.
func (s *Service) ExecuteMcpImport(id string) ([]McpTool, error) {
	tools, err := s.ImportToolsFromMcp(id)
	if err != nil {
		return nil, err
	}
	_, err = s.UpdateMcpServer(McpServer{ID: id, Tools: tools})
	if err != nil {
		return nil, err
	}
	return tools, nil
}

// mcpHeaders extracts MCP-related HTTP headers from the server's metadata.
// Keys with the "header:" prefix are forwarded as HTTP headers.
func (s *Service) mcpHeaders(srv *McpServer) map[string]string {
	if srv.Metadata == nil {
		return nil
	}
	headers := map[string]string{}
	for k, v := range srv.Metadata {
		if strings.HasPrefix(k, "header:") {
			headers[strings.TrimPrefix(k, "header:")] = v
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}
