package ai

// Prompt lifecycle operations. See service.go for the shared lifecycleOps
// helpers.

func (s *Service) promptOps() lifecycleOps { return lifecycleOps{store: s.prompts, typeName: "prompt"} }

// CreatePromptDraft opens a new draft on a prompt.
func (s *Service) CreatePromptDraft(id, name, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	return s.promptOps().createDraft(id, name, content, author, labels, bizTags, description, metadata)
}

// UpdatePromptDraft mutates an open prompt draft.
func (s *Service) UpdatePromptDraft(id, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	return s.promptOps().updateDraft(id, content, author, labels, bizTags, description, metadata)
}

// DeletePromptDraft removes an open prompt draft.
func (s *Service) DeletePromptDraft(id string) error { return s.promptOps().deleteDraft(id) }

// SubmitPrompt transitions a prompt draft to SUBMITTED.
func (s *Service) SubmitPrompt(id string) (*Resource, error) { return s.promptOps().submit(id) }

// PublishPrompt commits the prompt draft as a version. forcePublish bypasses
// the SUBMITTED requirement.
func (s *Service) PublishPrompt(id string, force bool) (*Resource, error) {
	return s.promptOps().publish(id, force)
}

// RedraftPrompt reopens a draft on a published prompt.
func (s *Service) RedraftPrompt(id, content, author string) (*Resource, error) {
	return s.promptOps().redraft(id, content, author)
}

// OnlinePrompt transitions a prompt to ONLINE.
func (s *Service) OnlinePrompt(id string) (*Resource, error) { return s.promptOps().online(id) }

// OfflinePrompt transitions a prompt to OFFLINE.
func (s *Service) OfflinePrompt(id string) (*Resource, error) { return s.promptOps().offline(id) }

// GetPrompt returns the prompt by ID.
func (s *Service) GetPrompt(id string) (*Resource, error) {
	return s.promptOps().requireResource(id)
}

// ListPrompts returns all prompts.
func (s *Service) ListPrompts() []*Resource { return s.prompts.list() }

// DeletePrompt removes a prompt.
func (s *Service) DeletePrompt(id string) error {
	if !s.prompts.delete(id) {
		return ErrResourceNotFound
	}
	return nil
}

// UpdatePromptLabels replaces the label set on a prompt.
func (s *Service) UpdatePromptLabels(id string, labels []string) (*Resource, error) {
	return s.promptOps().updateLabels(id, labels)
}

// BindPromptLabel adds a label to a prompt.
func (s *Service) BindPromptLabel(id, label string) (*Resource, error) {
	return s.promptOps().bindLabel(id, label)
}

// UnbindPromptLabel removes a label from a prompt.
func (s *Service) UnbindPromptLabel(id, label string) (*Resource, error) {
	return s.promptOps().unbindLabel(id, label)
}

// UpdatePromptBizTags replaces the biz tag set on a prompt.
func (s *Service) UpdatePromptBizTags(id string, tags []string) (*Resource, error) {
	return s.promptOps().updateBizTags(id, tags)
}

// UpdatePromptDescription sets the description on a prompt.
func (s *Service) UpdatePromptDescription(id, description string) (*Resource, error) {
	return s.promptOps().updateDescription(id, description)
}

// UpdatePromptMetadata merges metadata into a prompt.
func (s *Service) UpdatePromptMetadata(id string, metadata map[string]string) (*Resource, error) {
	return s.promptOps().updateMetadata(id, metadata)
}

// ListPromptVersions returns the published versions of a prompt.
func (s *Service) ListPromptVersions(id string) ([]Version, error) {
	r, err := s.GetPrompt(id)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Version, len(r.Versions))
	copy(out, r.Versions)
	return out, nil
}

// GetPromptVersion returns a specific prompt version.
func (s *Service) GetPromptVersion(id, version string) (Version, error) {
	r, err := s.GetPrompt(id)
	if err != nil {
		return Version{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.Versions {
		if v.Version == version {
			return v, nil
		}
	}
	return Version{}, ErrVersionNotFound
}

// DownloadPromptVersion returns the content of a specific prompt version.
func (s *Service) DownloadPromptVersion(id, version string) (string, error) {
	v, err := s.GetPromptVersion(id, version)
	if err != nil {
		return "", err
	}
	return v.Content, nil
}

// QueryClientPrompt returns the current online prompt content for a client.
// Returns ErrResourceNotFound if the prompt does not exist or is not online.
func (s *Service) QueryClientPrompt(id string) (*Resource, error) {
	r, err := s.GetPrompt(id)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.State != StateOnline {
		return nil, ErrResourceNotFound
	}
	return r, nil
}
