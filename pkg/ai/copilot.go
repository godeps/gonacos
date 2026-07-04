package ai

// Copilot endpoints. These wrap the pluggable LLMClient. When the client is
// the disabled default, every call returns ErrLLMDisabled.

// OptimizePromptStream asks the LLM to optimize a prompt.
func (s *Service) OptimizePromptStream(prompt string) (string, error) {
	return s.llm.OptimizePrompt(prompt)
}

// DebugPromptStream asks the LLM to debug a prompt.
func (s *Service) DebugPromptStream(prompt string) (string, error) {
	return s.llm.DebugPrompt(prompt)
}

// GenerateSkillStream asks the LLM to generate a skill from a description.
func (s *Service) GenerateSkillStream(description string) (string, error) {
	return s.llm.GenerateSkill(description)
}

// OptimizeSkillStream asks the LLM to optimize a skill.
func (s *Service) OptimizeSkillStream(skill string) (string, error) {
	return s.llm.OptimizeSkill(skill)
}
