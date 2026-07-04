// Package ai - top-level Service and specialized resource stores.
package ai

import (
	"sync"
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
	pipelines []Pipeline
	llm       LLMClient
}

// LLMClient is the pluggable LLM interface for copilot endpoints. The default
// implementation returns ErrLLMDisabled.
type LLMClient interface {
	OptimizePrompt(prompt string) (string, error)
	DebugPrompt(prompt string) (string, error)
	GenerateSkill(description string) (string, error)
	OptimizeSkill(skill string) (string, error)
}

// NewService creates an AI registry Service with the given LLM client. Pass
// nil to use the disabled default.
func NewService(llm LLMClient) *Service {
	if llm == nil {
		llm = disabledLLM{}
	}
	return &Service{
		prompts:   newResourceStore(),
		skills:    newResourceStore(),
		specs:     newResourceStore(),
		mcp:       newMcpStore(),
		a2a:       newA2AStore(),
		imports:   newImportStore(),
		pipelines: defaultPipelines(),
		llm:       llm,
	}
}

// Pipeline is the Nacos-compatible pipeline representation.
type Pipeline struct {
	ID          string            `json:"pipelineId"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Stages      []PipelineStage   `json:"stages,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
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
