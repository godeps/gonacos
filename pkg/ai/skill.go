package ai

// Skill lifecycle operations. Skills support upload (multipart) in addition to
// the draft workflow; the upload path stores the content directly as a new
// version without requiring a separate draft step.

func (s *Service) skillOps() lifecycleOps { return lifecycleOps{store: s.skills, typeName: "skill"} }

// CreateSkillDraft opens a new draft on a skill.
func (s *Service) CreateSkillDraft(id, name, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	return s.skillOps().createDraft(id, name, content, author, labels, bizTags, description, metadata)
}

// UpdateSkillDraft mutates an open skill draft.
func (s *Service) UpdateSkillDraft(id, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	return s.skillOps().updateDraft(id, content, author, labels, bizTags, description, metadata)
}

// DeleteSkillDraft removes an open skill draft.
func (s *Service) DeleteSkillDraft(id string) error { return s.skillOps().deleteDraft(id) }

// SubmitSkill transitions a skill draft to SUBMITTED.
func (s *Service) SubmitSkill(id string) (*Resource, error) { return s.skillOps().submit(id) }

// PublishSkill commits the skill draft as a version.
func (s *Service) PublishSkill(id string, force bool) (*Resource, error) {
	return s.skillOps().publish(id, force)
}

// RedraftSkill reopens a draft on a published skill.
func (s *Service) RedraftSkill(id, content, author string) (*Resource, error) {
	return s.skillOps().redraft(id, content, author)
}

// OnlineSkill transitions a skill to ONLINE.
func (s *Service) OnlineSkill(id string) (*Resource, error) { return s.skillOps().online(id) }

// OfflineSkill transitions a skill to OFFLINE.
func (s *Service) OfflineSkill(id string) (*Resource, error) { return s.skillOps().offline(id) }

// GetSkill returns the skill by ID.
func (s *Service) GetSkill(id string) (*Resource, error) {
	return s.skillOps().requireResource(id)
}

// ListSkills returns all skills.
func (s *Service) ListSkills() []*Resource { return s.skills.list() }

// DeleteSkill removes a skill.
func (s *Service) DeleteSkill(id string) error {
	if !s.skills.delete(id) {
		return ErrResourceNotFound
	}
	return nil
}

// UpdateSkillLabels replaces the label set on a skill.
func (s *Service) UpdateSkillLabels(id string, labels []string) (*Resource, error) {
	return s.skillOps().updateLabels(id, labels)
}

// UpdateSkillBizTags replaces the biz tag set on a skill.
func (s *Service) UpdateSkillBizTags(id string, tags []string) (*Resource, error) {
	return s.skillOps().updateBizTags(id, tags)
}

// UpdateSkillScope sets the scope on a skill.
func (s *Service) UpdateSkillScope(id, scope string) (*Resource, error) {
	return s.skillOps().updateScope(id, scope)
}

// ListSkillVersions returns the published versions of a skill.
func (s *Service) ListSkillVersions(id string) ([]Version, error) {
	r, err := s.GetSkill(id)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Version, len(r.Versions))
	copy(out, r.Versions)
	return out, nil
}

// GetSkillVersion returns a specific skill version.
func (s *Service) GetSkillVersion(id, version string) (Version, error) {
	r, err := s.GetSkill(id)
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

// DownloadSkillVersion returns the content of a specific skill version.
func (s *Service) DownloadSkillVersion(id, version string) (string, error) {
	v, err := s.GetSkillVersion(id, version)
	if err != nil {
		return "", err
	}
	return v.Content, nil
}

// UploadSkill uploads a skill as a new version directly, bypassing the draft
// workflow. This matches the Nacos console upload endpoint.
func (s *Service) UploadSkill(id, name, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	if _, err := s.skillOps().createDraft(id, name, content, author, labels, bizTags, description, metadata); err != nil && err != ErrDraftExists {
		return nil, err
	}
	return s.skillOps().publish(id, true)
}

// BatchUploadSkills uploads multiple skills in one call.
func (s *Service) BatchUploadSkills(items []UploadItem) (BatchUploadResult, error) {
	result := BatchUploadResult{}
	for _, item := range items {
		if _, err := s.UploadSkill(item.ID, item.Name, item.Content, item.Author, item.Labels, item.BizTags, item.Description, item.Metadata); err != nil {
			result.Failed = append(result.Failed, item.ID)
			continue
		}
		result.Uploaded = append(result.Uploaded, item.ID)
	}
	return result, nil
}

// UploadItem is a single skill upload entry.
type UploadItem struct {
	ID          string
	Name        string
	Content     string
	Author      string
	Labels      []string
	BizTags     []string
	Description string
	Metadata    map[string]string
}

// BatchUploadResult reports the success and failure of a batch upload.
type BatchUploadResult struct {
	Uploaded []string `json:"uploaded"`
	Failed   []string `json:"failed"`
}

// QueryClientSkill returns the current online skill content for a client.
func (s *Service) QueryClientSkill(id string) (*Resource, error) {
	r, err := s.GetSkill(id)
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
