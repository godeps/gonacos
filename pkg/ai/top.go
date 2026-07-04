// Package ai - top-level Service and specialized resource stores.
package ai

import (
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/ai/dify"
	"github.com/godeps/gonacos/pkg/ai/mcprouter"
	"github.com/godeps/gonacos/pkg/ai/plugin"
)

// Service is the top-level AI registry service. It owns the in-memory stores
// for each resource type and the LLM client used by copilot endpoints.
type Service struct {
	mu        sync.RWMutex
	prompts   *resourceStore
	skills    *resourceStore
	specs     *resourceStore
	mcp       *mcpStore
	a2a       *a2aStore
	imports   *importStore
	pipelines *pipelineStore
	apitomcp  *apitomcpStore
	templates *templateStore
	llm       LLMClient
	router    *mcprouter.Router
	dify      *dify.Client
	plugins   *plugin.Manager
}

// Option customizes a Service at construction time. Options are applied in
// order after the default stores are initialized.
type Option func(*Service)

// WithLLM sets the LLM client. Passing nil is a no-op so that callers can
// chain WithLLM(cfg) conditionally without nil checks.
func WithLLM(llm LLMClient) Option {
	return func(s *Service) {
		if llm != nil {
			s.llm = llm
		}
	}
}

// WithMcpRouter attaches a router that aggregates MCP backends behind a
// single streamable HTTP endpoint.
func WithMcpRouter(r *mcprouter.Router) Option {
	return func(s *Service) {
		s.router = r
	}
}

// McpRouter returns the attached MCP router, or nil if none was configured.
func (s *Service) McpRouter() *mcprouter.Router {
	if s == nil {
		return nil
	}
	return s.router
}

// WithDify attaches a Dify client for workflow integration.
func WithDify(c *dify.Client) Option {
	return func(s *Service) {
		s.dify = c
	}
}

// DifyClient returns the attached Dify client, or nil if none was configured.
func (s *Service) DifyClient() *dify.Client {
	if s == nil {
		return nil
	}
	return s.dify
}

// SetDifyClient replaces the attached Dify client at runtime. Pass nil to
// detach. This is safe because DifyClient() returns the pointer; callers
// capture the pointer for the duration of a request.
func (s *Service) SetDifyClient(c *dify.Client) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dify = c
}

// WithPlugins attaches a plugin manager. If the manager is non-nil, its
// plugins are also started when the Service is constructed.
func WithPlugins(m *plugin.Manager) Option {
	return func(s *Service) {
		s.plugins = m
	}
}

// Plugins returns the attached plugin manager, or nil if none was configured.
func (s *Service) Plugins() *plugin.Manager {
	if s == nil {
		return nil
	}
	return s.plugins
}

// LLMClient is the pluggable LLM interface for copilot endpoints. The default
// implementation returns ErrLLMDisabled.
type LLMClient interface {
	OptimizePrompt(prompt string) (string, error)
	DebugPrompt(prompt string) (string, error)
	GenerateSkill(description string) (string, error)
	OptimizeSkill(skill string) (string, error)
}

// NewService creates an AI registry Service. The first argument is the
// LLM client (pass nil to use the disabled default); subsequent options
// customize the service further.
//
// NewService(nil) remains supported for backwards compatibility.
func NewService(llm LLMClient, opts ...Option) *Service {
	if llm == nil {
		llm = disabledLLM{}
	}
	s := &Service{
		prompts:   newResourceStore(),
		skills:    newResourceStore(),
		specs:     newResourceStore(),
		mcp:       newMcpStore(),
		a2a:       newA2AStore(),
		imports:   newImportStore(),
		pipelines: newPipelineStore(defaultPipelines()),
		apitomcp:  newApitomcpStore(),
		templates: newTemplateStore(),
		llm:       llm,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Pipeline is the Nacos-compatible pipeline representation.
type Pipeline struct {
	ID          string            `json:"pipelineId"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Stages      []PipelineStage   `json:"stages,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"createdAt,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt,omitempty"`
}

// PipelineStage is a single pipeline stage.
type PipelineStage struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config,omitempty"`
}

func defaultPipelines() []Pipeline {
	return []Pipeline{
		{ID: "default", Name: "Default Pipeline", Description: "Default AI pipeline"},
	}
}

// disabledLLM returns ErrLLMDisabled for every call.
type disabledLLM struct{}

func (disabledLLM) OptimizePrompt(string) (string, error) { return "", ErrLLMDisabled }
func (disabledLLM) DebugPrompt(string) (string, error)    { return "", ErrLLMDisabled }
func (disabledLLM) GenerateSkill(string) (string, error)  { return "", ErrLLMDisabled }
func (disabledLLM) OptimizeSkill(string) (string, error)  { return "", ErrLLMDisabled }

// md5Hex is a small wrapper to avoid importing crypto/md5 in service.go
// directly; the implementation lives in hash.go.
var md5Hex = md5HexImpl
