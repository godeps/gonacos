package ai

import (
	"encoding/json"
	"testing"
)

func TestAISnapshotRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewService(nil)

	prompt, err := s.CreatePromptDraft("p1", "greeting", "Hello {{name}}", "alice", []string{"chat"}, []string{"v1"}, "greeting prompt", nil)
	if err != nil {
		t.Fatalf("create prompt draft: %v", err)
	}
	if _, err := s.SubmitPrompt(prompt.ID); err != nil {
		t.Fatalf("submit prompt: %v", err)
	}
	if _, err := s.PublishPrompt(prompt.ID, true); err != nil {
		t.Fatalf("publish prompt: %v", err)
	}
	if _, err := s.OnlinePrompt(prompt.ID); err != nil {
		t.Fatalf("online prompt: %v", err)
	}

	skill, err := s.CreateSkillDraft("s1", "echo", "def echo(): pass", "bob", nil, nil, "echo skill", nil)
	if err != nil {
		t.Fatalf("create skill draft: %v", err)
	}
	_ = skill

	mcp, err := s.CreateMcpServer(McpServer{
		ID:       "m1",
		Name:     "weather",
		Protocol: "http",
		Endpoint: "http://weather.local/mcp",
	})
	if err != nil {
		t.Fatalf("create mcp: %v", err)
	}
	_ = mcp

	a2a, err := s.RegisterA2AAgent(A2AAgent{
		ID:        "a1",
		Name:      "assistant",
		Endpoint:  "http://assistant.local",
		Protocol:  "jsonrpc",
		Version:   "1.0",
	})
	if err != nil {
		t.Fatalf("create a2a: %v", err)
	}
	_ = a2a

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if s.SnapshotKey() != "ai" {
		t.Fatalf("key = %v", s.SnapshotKey())
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	restored := NewService(nil)
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}

	prompts := restored.ListPrompts()
	if len(prompts) != 1 {
		t.Fatalf("prompts = %d, want 1", len(prompts))
	}
	if prompts[0].Name != "greeting" {
		t.Fatalf("prompt name = %v", prompts[0].Name)
	}
	clientPrompt, err := restored.QueryClientPrompt("p1")
	if err != nil {
		t.Fatalf("query client prompt: %v", err)
	}
	if clientPrompt.Name != "greeting" {
		t.Fatalf("client prompt name = %v", clientPrompt.Name)
	}
	skills := restored.ListSkills()
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want 1", len(skills))
	}
	mcps := restored.ListMcpServers()
	if len(mcps) != 1 {
		t.Fatalf("mcp = %d, want 1", len(mcps))
	}
	a2as := restored.ListA2AAgents()
	if len(a2as) != 1 {
		t.Fatalf("a2a = %d, want 1", len(a2as))
	}
}

func TestAISnapshotEmptyService(t *testing.T) {
	t.Parallel()
	s := NewService(nil)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	raw, _ := json.Marshal(snap)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	restored := NewService(nil)
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore empty: %v", err)
	}
}
