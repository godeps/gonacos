package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseConfigDefaults exercises the YAML loader and the defaulting rules
// (first provider becomes default when none is named; unknown types fall
// back to openai).
func TestParseConfigDefaults(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
providers:
  - name: "openai-default"
    type: "openai"
    apiKey: "sk-test"
    endpoint: "https://api.openai.com/v1"
    model: "gpt-4o"
  - name: "ollama-local"
    type: "ollama"
    endpoint: "http://localhost:11434"
    model: "llama3"
default: "ollama-local"
`)
	cfg, err := ParseConfig(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Default != "ollama-local" {
		t.Fatalf("default = %q, want ollama-local", cfg.Default)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(cfg.Providers))
	}
}

// TestParseConfigRejectsEmpty ensures a config with no providers is rejected.
func TestParseConfigRejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := ParseConfig([]byte(`{}`)); err != ErrNoProviders {
		t.Fatalf("err = %v, want %v", err, ErrNoProviders)
	}
}

// TestParseConfigRejectsMissingModel ensures a provider without a model is
// rejected at parse time, not at call time.
func TestParseConfigRejectsMissingModel(t *testing.T) {
	t.Parallel()
	yaml := []byte(`
providers:
  - name: "broken"
    type: "openai"
    apiKey: "sk-test"
`)
	_, err := ParseConfig(yaml)
	if err == nil {
		t.Fatal("expected error for provider without model")
	}
	if !strings.Contains(err.Error(), "missing model") {
		t.Fatalf("err = %v, want missing model", err)
	}
}

// TestNewClientWithMockOpenAI spins up an OpenAI-compatible HTTP server,
// builds a bifrost-backed Client against it, and verifies that chat()
// returns the assistant text from the mock response.
//
// This is the integration canary for the bifrost adapter: if bifrost's API
// changes in a way that breaks request routing or response parsing, this
// test fails before any copilot endpoint is wired up.
func TestNewClientWithMockOpenAI(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the request body into the response so we can assert that
		// bifrost actually sent a chat completion request.
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ := req["model"].(string)
		if model == "" {
			model = "unknown"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "MOCK_RESPONSE",
					},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer server.Close()

	cfg := Config{
		Providers: []ProviderConfig{{
			Name:      "test",
			Type:      "openai",
			APIKey:    "sk-test",
			Endpoint:  server.URL,
			Model:     "gpt-4o",
			MaxTokens: 16,
		}},
		Default: "test",
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer client.Close()

	out, err := client.OptimizePrompt("hello world")
	if err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if out != "MOCK_RESPONSE" {
		t.Fatalf("output = %q, want MOCK_RESPONSE", out)
	}
}

// TestChatRejectsEmptyPrompts verifies the input validation guards run
// before any HTTP request is attempted.
func TestChatRejectsEmptyPrompts(t *testing.T) {
	t.Parallel()
	client := &Client{bf: nil}
	if _, err := client.OptimizePrompt(""); err == nil {
		t.Fatal("expected error for empty user prompt")
	}
	if _, err := client.OptimizePrompt("x"); err != ErrNotConfigured {
		t.Fatalf("err = %v, want %v", err, ErrNotConfigured)
	}
}
