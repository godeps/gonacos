package llm

import "errors"

var (
	// ErrNoProviders is returned when a Config has no providers.
	ErrNoProviders = errors.New("llm: no providers configured")
	// ErrInvalidConfig is returned when a Config has structural problems.
	ErrInvalidConfig = errors.New("llm: invalid config")
	// ErrNotConfigured is returned when a Client is used before NewClient.
	ErrNotConfigured = errors.New("llm: client not configured")
	// ErrEmptyResponse is returned when the provider returned no content.
	ErrEmptyResponse = errors.New("llm: empty response from provider")
)
