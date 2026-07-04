package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// Client is the gonacos LLM client backed by bifrost. It implements the
// ai.LLMClient interface (OptimizePrompt/DebugPrompt/GenerateSkill/OptimizeSkill)
// by routing each call through bifrost's ChatCompletionRequest with a fixed
// system prompt.
type Client struct {
	bf               *bifrost.Bifrost
	defaultProvider  schemas.ModelProvider
	defaultModel     string
	defaultTemp      float64
	defaultMaxTokens int
}

// NewClient builds a bifrost-backed LLM client from a Config. The returned
// client must be closed with Close when no longer in use.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Default == "" {
		cfg.Default = cfg.Providers[0].Name
	}
	defProv, _ := cfg.findProvider(cfg.Default)
	acct, err := newStaticAccount(&cfg)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	bf, err := bifrost.Init(ctx, schemas.BifrostConfig{
		Account:         acct,
		InitialPoolSize: 16,
	})
	if err != nil {
		return nil, fmt.Errorf("bifrost init: %w", err)
	}
	return &Client{
		bf:               bf,
		defaultProvider:  schemas.ModelProvider(defProv.Type),
		defaultModel:     defProv.Model,
		defaultTemp:      defProv.Temperature,
		defaultMaxTokens: defProv.MaxTokens,
	}, nil
}

// Close releases bifrost resources. It is safe to call on a nil receiver or
// after a failed NewClient.
func (c *Client) Close() {
	if c == nil || c.bf == nil {
		return
	}
	c.bf.Shutdown()
	c.bf = nil
}

// chat sends a system+user message pair and returns the assistant's text
// response. It is the shared backbone for all four LLMClient methods.
func (c *Client) chat(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if c == nil || c.bf == nil {
		return "", ErrNotConfigured
	}
	if systemPrompt == "" {
		return "", errors.New("llm: system prompt is required")
	}
	if userPrompt == "" {
		return "", errors.New("llm: user prompt is required")
	}
	bctx, cancel := schemas.NewBifrostContextWithTimeout(ctx, 120*time.Second)
	defer cancel()

	sysContent := systemPrompt
	userContent := userPrompt
	messages := []schemas.ChatMessage{
		{
			Role:    schemas.ChatMessageRoleSystem,
			Content: &schemas.ChatMessageContent{ContentStr: &sysContent},
		},
		{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: &userContent},
		},
	}
	params := &schemas.ChatParameters{}
	if c.defaultTemp > 0 {
		t := c.defaultTemp
		params.Temperature = &t
	}
	if c.defaultMaxTokens > 0 {
		m := c.defaultMaxTokens
		params.MaxCompletionTokens = &m
	}

	req := &schemas.BifrostChatRequest{
		Provider: c.defaultProvider,
		Model:    c.defaultModel,
		Input:    messages,
		Params:   params,
	}
	resp, berr := c.bf.ChatCompletionRequest(bctx, req)
	if berr != nil {
		return "", fmt.Errorf("bifrost chat: %s", bifrostErrorMsg(berr))
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", ErrEmptyResponse
	}
	choice := resp.Choices[0]
	if choice.ChatNonStreamResponseChoice == nil || choice.Message == nil {
		return "", ErrEmptyResponse
	}
	msg := choice.Message
	if msg.Content == nil || msg.Content.ContentStr == nil || *msg.Content.ContentStr == "" {
		return "", ErrEmptyResponse
	}
	return *msg.Content.ContentStr, nil
}

func bifrostErrorMsg(berr *schemas.BifrostError) string {
	if berr == nil {
		return "<nil>"
	}
	if berr.Error != nil && berr.Error.Message != "" {
		return berr.Error.Message
	}
	if berr.Type != nil {
		return *berr.Type
	}
	return "<unknown>"
}

// OptimizePrompt refines a prompt for clarity and effectiveness.
func (c *Client) OptimizePrompt(prompt string) (string, error) {
	return c.chat(context.Background(), systemOptimizePrompt, prompt)
}

// DebugPrompt analyzes a prompt for ambiguity and suggests fixes.
func (c *Client) DebugPrompt(prompt string) (string, error) {
	return c.chat(context.Background(), systemDebugPrompt, prompt)
}

// GenerateSkill produces a skill YAML from a natural-language description.
func (c *Client) GenerateSkill(description string) (string, error) {
	return c.chat(context.Background(), systemGenerateSkill, description)
}

// OptimizeSkill refines an existing skill YAML definition.
func (c *Client) OptimizeSkill(skill string) (string, error) {
	return c.chat(context.Background(), systemOptimizeSkill, skill)
}

const (
	systemOptimizePrompt = `You are a prompt optimization expert. Refine the user's prompt for clarity, specificity, and effectiveness. Preserve the original intent. Return only the optimized prompt, with no explanations or surrounding text.`
	systemDebugPrompt    = `You are a prompt debugging expert. Analyze the user's prompt for ambiguity, contradictions, and improvement areas. Return a concise diagnosis followed by a suggested fix, formatted as: "Diagnosis: ...\nFix: ...".`
	systemGenerateSkill  = `You are a skill generation expert for the Nacos AI platform. Based on the user's description, produce a complete skill definition in YAML format with fields: name, description, version, inputs, outputs, and template. Return only the YAML, no markdown fences or commentary.`
	systemOptimizeSkill  = `You are a skill optimization expert for the Nacos AI platform. Refine the user's skill YAML for clarity, correctness, and completeness. Preserve the original intent. Return only the optimized YAML, no markdown fences or commentary.`
)
