package ai

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godeps/gonacos/pkg/ai/mcptemplate"
)

// UserTemplate wraps mcptemplate.Template with audit timestamps. Stored
// templates are user-defined; builtins live in mcptemplate.BuiltinTemplates
// and are immutable.
type UserTemplate struct {
	mcptemplate.Template
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// templateStore owns user-defined templates.
type templateStore struct {
	mu        sync.RWMutex
	templates map[string]*UserTemplate
}

func newTemplateStore() *templateStore {
	return &templateStore{templates: map[string]*UserTemplate{}}
}

func (s *templateStore) get(id string) (*UserTemplate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	return t, ok
}

func (s *templateStore) list() []*UserTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*UserTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *templateStore) put(t *UserTemplate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates[t.ID] = t
}

func (s *templateStore) delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[id]; !ok {
		return false
	}
	delete(s.templates, id)
	return true
}

func (s *templateStore) replace(list []UserTemplate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates = map[string]*UserTemplate{}
	for i := range list {
		c := list[i]
		s.templates[c.ID] = &c
	}
}

// isBuiltinTemplateID returns true if the ID collides with a builtin.
func isBuiltinTemplateID(id string) bool {
	for i := range mcptemplate.BuiltinTemplates {
		if mcptemplate.BuiltinTemplates[i].ID == id {
			return true
		}
	}
	return false
}

// ListTemplates returns builtin templates followed by user templates.
func (s *Service) ListTemplates() []mcptemplate.Template {
	if s == nil || s.templates == nil {
		out := make([]mcptemplate.Template, len(mcptemplate.BuiltinTemplates))
		copy(out, mcptemplate.BuiltinTemplates)
		return out
	}
	out := make([]mcptemplate.Template, 0, len(mcptemplate.BuiltinTemplates)+len(s.templates.list()))
	out = append(out, mcptemplate.BuiltinTemplates...)
	for _, t := range s.templates.list() {
		out = append(out, t.Template)
	}
	return out
}

// GetTemplate returns a template by ID (builtin or user).
func (s *Service) GetTemplate(id string) (mcptemplate.Template, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return mcptemplate.Template{}, ErrTemplateIDRequired
	}
	if b := mcptemplate.FindBuiltin(id); b != nil {
		return *b, nil
	}
	if s != nil && s.templates != nil {
		if t, ok := s.templates.get(id); ok {
			return t.Template, nil
		}
	}
	return mcptemplate.Template{}, ErrTemplateNotFound
}

// CreateTemplate stores a new user template. The ID must not collide with a
// builtin.
func (s *Service) CreateTemplate(t mcptemplate.Template) (mcptemplate.Template, error) {
	t.ID = strings.TrimSpace(t.ID)
	if t.ID == "" {
		return mcptemplate.Template{}, ErrTemplateIDRequired
	}
	if strings.TrimSpace(t.Body) == "" {
		return mcptemplate.Template{}, ErrTemplateBodyRequired
	}
	if isBuiltinTemplateID(t.ID) {
		return mcptemplate.Template{}, fmt.Errorf("%w: %s", ErrTemplateBuiltinID, t.ID)
	}
	if _, ok := s.templates.get(t.ID); ok {
		return mcptemplate.Template{}, fmt.Errorf("%w: %s", ErrTemplateExists, t.ID)
	}
	now := time.Now()
	ut := &UserTemplate{Template: t, CreatedAt: now, UpdatedAt: now}
	s.templates.put(ut)
	return ut.Template, nil
}

// UpdateTemplate replaces an existing user template. Builtins are immutable.
func (s *Service) UpdateTemplate(t mcptemplate.Template) (mcptemplate.Template, error) {
	t.ID = strings.TrimSpace(t.ID)
	if t.ID == "" {
		return mcptemplate.Template{}, ErrTemplateIDRequired
	}
	if strings.TrimSpace(t.Body) == "" {
		return mcptemplate.Template{}, ErrTemplateBodyRequired
	}
	if isBuiltinTemplateID(t.ID) {
		return mcptemplate.Template{}, fmt.Errorf("%w: %s", ErrTemplateBuiltinImmutable, t.ID)
	}
	existing, ok := s.templates.get(t.ID)
	if !ok {
		return mcptemplate.Template{}, ErrTemplateNotFound
	}
	ut := &UserTemplate{
		Template:  t,
		CreatedAt: existing.CreatedAt,
		UpdatedAt: time.Now(),
	}
	s.templates.put(ut)
	return ut.Template, nil
}

// DeleteTemplate removes a user template. Builtins cannot be deleted.
func (s *Service) DeleteTemplate(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTemplateIDRequired
	}
	if isBuiltinTemplateID(id) {
		return fmt.Errorf("%w: %s", ErrTemplateBuiltinImmutable, id)
	}
	if !s.templates.delete(id) {
		return ErrTemplateNotFound
	}
	return nil
}

// RenderTemplate renders the template with the given values and returns the
// YAML bytes. Does not persist anything.
func (s *Service) RenderTemplate(id string, values map[string]string) ([]byte, error) {
	tmpl, err := s.GetTemplate(id)
	if err != nil {
		return nil, err
	}
	return mcptemplate.Render(tmpl, values)
}

// InstantiateTemplate renders the template and creates an apitomcp config
// from the result. The config name is derived from the rendered YAML's
// server.name.
func (s *Service) InstantiateTemplate(id string, values map[string]string) (*ApitomcpConfig, error) {
	yamlBytes, err := s.RenderTemplate(id, values)
	if err != nil {
		return nil, err
	}
	return s.CreateApitomcpConfig(string(yamlBytes), "instantiated from template "+id)
}

var (
	// ErrTemplateNotFound is returned when a template is missing.
	ErrTemplateNotFound = errors.New("template: not found")
	// ErrTemplateExists is returned when a template with the same ID exists.
	ErrTemplateExists = errors.New("template: already exists")
	// ErrTemplateIDRequired is returned when the template ID is empty.
	ErrTemplateIDRequired = errors.New("template: id is required")
	// ErrTemplateBodyRequired is returned when the template body is empty.
	ErrTemplateBodyRequired = errors.New("template: body is required")
	// ErrTemplateBuiltinID is returned when creating a template with a builtin ID.
	ErrTemplateBuiltinID = errors.New("template: id collides with builtin")
	// ErrTemplateBuiltinImmutable is returned when updating/deleting a builtin.
	ErrTemplateBuiltinImmutable = errors.New("template: builtin templates are immutable")
)
