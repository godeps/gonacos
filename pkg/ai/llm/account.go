package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// staticAccount implements schemas.Account for a fixed set of providers
// loaded from YAML. It does not support dynamic key rotation, vault
// integration, or any of bifrost's advanced account features — it is the
// simplest possible adapter that lets bifrost route to a configured
// provider.
type staticAccount struct {
	providers map[schemas.ModelProvider]*providerEntry
	order     []schemas.ModelProvider
}

type providerEntry struct {
	provider ProviderConfig
	config   schemas.ProviderConfig
	keys     []schemas.Key
}

func newStaticAccount(cfg *Config) (*staticAccount, error) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return nil, ErrNoProviders
	}
	acct := &staticAccount{
		providers: map[schemas.ModelProvider]*providerEntry{},
	}
	for _, p := range cfg.Providers {
		key := schemas.ModelProvider(p.Type)
		if _, ok := standardProviders[string(key)]; !ok {
			// Treat unknown types as OpenAI-compatible. bifrost's openai
			// provider accepts arbitrary base URLs, which covers Ollama,
			// vLLM, and other OpenAI-compatible gateways.
			key = schemas.ModelProvider("openai")
		}
		if _, exists := acct.providers[key]; exists {
			return nil, fmt.Errorf("%w: duplicate provider type %q", ErrInvalidConfig, key)
		}
		entry, err := buildProviderEntry(p, key)
		if err != nil {
			return nil, err
		}
		acct.providers[key] = entry
		acct.order = append(acct.order, key)
	}
	return acct, nil
}

func buildProviderEntry(p ProviderConfig, key schemas.ModelProvider) (*providerEntry, error) {
	nc := schemas.NetworkConfig{
		BaseURL:                        p.Endpoint,
		DefaultRequestTimeoutInSeconds: 60,
		MaxRetries:                     2,
		RetryBackoffInitial:            500 * time.Millisecond,
		RetryBackoffMax:                5 * time.Second,
	}
	pcfg := schemas.ProviderConfig{
		NetworkConfig: nc,
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: 4,
			BufferSize:  16,
		},
	}
	if string(key) == "openai" {
		pcfg.OpenAIConfig = &schemas.OpenAIConfig{}
	}
	enabled := true
	k := schemas.Key{
		ID:     p.Name,
		Name:   p.Name,
		Value:  *schemas.NewSecretVar(p.APIKey),
		Models: schemas.WhiteList{"*"},
		Weight: 1.0,
		Enabled: &enabled,
	}
	return &providerEntry{
		provider: p,
		config:   pcfg,
		keys:     []schemas.Key{k},
	}, nil
}

func (a *staticAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, len(a.order))
	copy(out, a.order)
	return out, nil
}

func (a *staticAccount) GetKeysForProvider(_ context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	entry, ok := a.providers[providerKey]
	if !ok {
		return nil, fmt.Errorf("provider %s not configured", providerKey)
	}
	out := make([]schemas.Key, len(entry.keys))
	copy(out, entry.keys)
	return out, nil
}

func (a *staticAccount) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	entry, ok := a.providers[providerKey]
	if !ok {
		return nil, fmt.Errorf("provider %s not configured", providerKey)
	}
	cfg := entry.config
	return &cfg, nil
}

// standardProviders is the set of bifrost provider keys gonacos supports
// directly. Anything not in this set falls back to OpenAI-compatible.
var standardProviders = map[string]struct{}{
	"openai":    {},
	"anthropic": {},
	"ollama":    {},
	"cohere":    {},
	"mistral":   {},
	"bedrock":   {},
	"vertex":    {},
	"azure":     {},
}
