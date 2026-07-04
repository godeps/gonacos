package ai

// AgentSpec lifecycle operations. AgentSpecs support upload and a resource
// tree view in addition to the standard versioned resource lifecycle.

func (s *Service) specOps() lifecycleOps { return lifecycleOps{store: s.specs, typeName: "agentspec"} }

// CreateAgentSpecDraft opens a new draft on an AgentSpec.
func (s *Service) CreateAgentSpecDraft(id, name, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	return s.specOps().createDraft(id, name, content, author, labels, bizTags, description, metadata)
}

// UpdateAgentSpecDraft mutates an open AgentSpec draft.
func (s *Service) UpdateAgentSpecDraft(id, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	return s.specOps().updateDraft(id, content, author, labels, bizTags, description, metadata)
}

// DeleteAgentSpecDraft removes an open AgentSpec draft.
func (s *Service) DeleteAgentSpecDraft(id string) error { return s.specOps().deleteDraft(id) }

// SubmitAgentSpec transitions an AgentSpec draft to SUBMITTED.
func (s *Service) SubmitAgentSpec(id string) (*Resource, error) { return s.specOps().submit(id) }

// PublishAgentSpec commits the AgentSpec draft as a version.
func (s *Service) PublishAgentSpec(id string, force bool) (*Resource, error) {
	return s.specOps().publish(id, force)
}

// RedraftAgentSpec reopens a draft on a published AgentSpec.
func (s *Service) RedraftAgentSpec(id, content, author string) (*Resource, error) {
	return s.specOps().redraft(id, content, author)
}

// OnlineAgentSpec transitions an AgentSpec to ONLINE.
func (s *Service) OnlineAgentSpec(id string) (*Resource, error) { return s.specOps().online(id) }

// OfflineAgentSpec transitions an AgentSpec to OFFLINE.
func (s *Service) OfflineAgentSpec(id string) (*Resource, error) { return s.specOps().offline(id) }

// GetAgentSpec returns the AgentSpec by ID.
func (s *Service) GetAgentSpec(id string) (*Resource, error) {
	return s.specOps().requireResource(id)
}

// ListAgentSpecs returns all AgentSpecs.
func (s *Service) ListAgentSpecs() []*Resource { return s.specs.list() }

// DeleteAgentSpec removes an AgentSpec.
func (s *Service) DeleteAgentSpec(id string) error {
	if !s.specs.delete(id) {
		return ErrResourceNotFound
	}
	return nil
}

// UpdateAgentSpecLabels replaces the label set on an AgentSpec.
func (s *Service) UpdateAgentSpecLabels(id string, labels []string) (*Resource, error) {
	return s.specOps().updateLabels(id, labels)
}

// UpdateAgentSpecBizTags replaces the biz tag set on an AgentSpec.
func (s *Service) UpdateAgentSpecBizTags(id string, tags []string) (*Resource, error) {
	return s.specOps().updateBizTags(id, tags)
}

// UpdateAgentSpecScope sets the scope on an AgentSpec.
func (s *Service) UpdateAgentSpecScope(id, scope string) (*Resource, error) {
	return s.specOps().updateScope(id, scope)
}

// ListAgentSpecVersions returns the published versions of an AgentSpec.
func (s *Service) ListAgentSpecVersions(id string) ([]Version, error) {
	r, err := s.GetAgentSpec(id)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Version, len(r.Versions))
	copy(out, r.Versions)
	return out, nil
}

// GetAgentSpecVersion returns a specific AgentSpec version.
func (s *Service) GetAgentSpecVersion(id, version string) (Version, error) {
	r, err := s.GetAgentSpec(id)
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

// UploadAgentSpec uploads an AgentSpec as a new version directly.
func (s *Service) UploadAgentSpec(id, name, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	if _, err := s.specOps().createDraft(id, name, content, author, labels, bizTags, description, metadata); err != nil && err != ErrDraftExists {
		return nil, err
	}
	return s.specOps().publish(id, true)
}

// QueryClientAgentSpecs returns all online AgentSpecs for a client.
func (s *Service) QueryClientAgentSpecs() []*Resource {
	out := []*Resource{}
	for _, r := range s.specs.list() {
		r.mu.RLock()
		if r.State == StateOnline {
			out = append(out, r)
		}
		r.mu.RUnlock()
	}
	return out
}

// SearchClientAgentSpecs returns online AgentSpecs whose name or description
// matches the query.
func (s *Service) SearchClientAgentSpecs(query string) []*Resource {
	all := s.QueryClientAgentSpecs()
	if query == "" {
		return all
	}
	out := []*Resource{}
	for _, r := range all {
		r.mu.RLock()
		if containsString([]string{r.Name, r.Description}, query) {
			out = append(out, r)
		}
		r.mu.RUnlock()
	}
	return out
}
