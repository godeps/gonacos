// Package ai implements the Nacos v3 AI Registry: prompt, skill, AgentSpec,
// MCP server, A2A agent, import sources, pipelines, and copilot endpoints.
//
// The Service type owns an in-memory registry of AI resources. Each resource
// type shares a common lifecycle state machine:
//
//	draft -> submitted -> published -> online/offline
//	  ^                                        |
//	  |________________ redraft _______________|
//
// LLM calls in copilot endpoints are pluggable via the LLMClient interface and
// disabled by default in tests by returning ErrLLMDisabled.
package ai
