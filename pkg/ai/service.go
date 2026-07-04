package ai

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ResourceState is the lifecycle state of an AI resource.
type ResourceState string

const (
	StateDraft     ResourceState = "DRAFT"
	StateSubmitted ResourceState = "SUBMITTED"
	StatePublished ResourceState = "PUBLISHED"
	StateOnline    ResourceState = "ONLINE"
	StateOffline   ResourceState = "OFFLINE"
)

var (
	ErrMissingID           = errors.New("id is required")
	ErrMissingName         = errors.New("name is required")
	ErrMissingVersion      = errors.New("version is required")
	ErrMissingContent      = errors.New("content is required")
	ErrResourceExists      = errors.New("resource already exists")
	ErrResourceNotFound    = errors.New("resource not found")
	ErrVersionNotFound     = errors.New("version not found")
	ErrDraftExists         = errors.New("draft already exists")
	ErrDraftNotFound       = errors.New("draft not found")
	ErrInvalidState        = errors.New("invalid state for operation")
	ErrLLMDisabled         = errors.New("LLM client is disabled")
	ErrImportSourceUnknown = errors.New("import source not found")
)

// Version is a published version of an AI resource.
type Version struct {
	Version     string            `json:"version"`
	Content     string            `json:"-"`
	Author      string            `json:"author,omitempty"`
	PublishedAt time.Time         `json:"publishedAt"`
	Labels      []string          `json:"labels,omitempty"`
	BizTags     []string          `json:"bizTags,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	MD5         string            `json:"md5,omitempty"`
}

// Resource is the common AI resource representation. Specialized types embed
// this and add their own fields.
type Resource struct {
	mu             sync.RWMutex
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	State          ResourceState     `json:"state"`
	Owner          string            `json:"owner,omitempty"`
	Description    string            `json:"description,omitempty"`
	Labels         []string          `json:"labels,omitempty"`
	BizTags        []string          `json:"bizTags,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Draft          *Draft            `json:"draft,omitempty"`
	Versions       []Version         `json:"versions,omitempty"`
	CurrentVersion string            `json:"currentVersion,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

// Draft is an in-progress unpublished revision.
type Draft struct {
	Version     string            `json:"version"`
	Content     string            `json:"-"`
	Author      string            `json:"author,omitempty"`
	Labels      []string          `json:"labels,omitempty"`
	BizTags     []string          `json:"bizTags,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// HasDraft reports whether the resource currently has an open draft.
func (r *Resource) HasDraft() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Draft != nil
}

// resourceStore is the generic in-memory store for one resource type.
type resourceStore struct {
	mu        sync.RWMutex
	resources map[string]*Resource
}

func newResourceStore() *resourceStore {
	return &resourceStore{resources: map[string]*Resource{}}
}

func (s *resourceStore) list() []*Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Resource, 0, len(s.resources))
	for _, r := range s.resources {
		out = append(out, r)
	}
	return out
}

func (s *resourceStore) delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.resources[id]; !ok {
		return false
	}
	delete(s.resources, id)
	return true
}

// lifecycleOps are the shared lifecycle operations for versioned resources.
// Each resource type (prompt, skill, agentspec) calls these with its own
// store and type name.
type lifecycleOps struct {
	store    *resourceStore
	typeName string
}

// createDraft opens a new draft on an existing resource, or creates the
// resource if it does not exist (matching Nacos behavior).
func (l lifecycleOps) createDraft(id, name, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	if content == "" {
		return nil, ErrMissingContent
	}

	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	r, ok := l.store.resources[id]
	if !ok {
		if name = strings.TrimSpace(name); name == "" {
			return nil, ErrMissingName
		}
		r = &Resource{
			ID:        id,
			Name:      name,
			Type:      l.typeName,
			State:     StateDraft,
			CreatedAt: time.Now(),
		}
		l.store.resources[id] = r
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Draft != nil {
		return nil, ErrDraftExists
	}
	now := time.Now()
	r.Draft = &Draft{
		Version:     nextVersion(r),
		Content:     content,
		Author:      author,
		Labels:      cloneStrings(labels),
		BizTags:     cloneStrings(bizTags),
		Description: description,
		Metadata:    cloneMetadata(metadata),
		UpdatedAt:   now,
	}
	r.State = StateDraft
	r.UpdatedAt = now
	return r, nil
}

// updateDraft mutates the content and metadata of an open draft.
func (l lifecycleOps) updateDraft(id, content, author string, labels, bizTags []string, description string, metadata map[string]string) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	r, ok := l.store.resources[id]
	if !ok {
		return nil, ErrResourceNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Draft == nil {
		return nil, ErrDraftNotFound
	}
	if content != "" {
		r.Draft.Content = content
	}
	r.Draft.Author = author
	if labels != nil {
		r.Draft.Labels = cloneStrings(labels)
	}
	if bizTags != nil {
		r.Draft.BizTags = cloneStrings(bizTags)
	}
	if description != "" {
		r.Draft.Description = description
	}
	if metadata != nil {
		r.Draft.Metadata = cloneMetadata(metadata)
	}
	r.Draft.UpdatedAt = time.Now()
	r.UpdatedAt = r.Draft.UpdatedAt
	return r, nil
}

// deleteDraft removes the open draft. The resource stays registered.
func (l lifecycleOps) deleteDraft(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrMissingID
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	r, ok := l.store.resources[id]
	if !ok {
		return ErrResourceNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Draft == nil {
		return ErrDraftNotFound
	}
	r.Draft = nil
	if r.State == StateDraft {
		r.State = lastState(r)
	}
	r.UpdatedAt = time.Now()
	return nil
}

// submit transitions a draft to the SUBMITTED state.
func (l lifecycleOps) submit(id string) (*Resource, error) {
	r, err := l.requireDraft(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Draft == nil {
		return nil, ErrDraftNotFound
	}
	r.State = StateSubmitted
	r.UpdatedAt = time.Now()
	return r, nil
}

// publish commits the current draft as a new version and transitions to
// PUBLISHED. forcePublish does the same without requiring SUBMITTED state.
func (l lifecycleOps) publish(id string, force bool) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	r, ok := l.store.resources[id]
	if !ok {
		return nil, ErrResourceNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Draft == nil {
		return nil, ErrDraftNotFound
	}
	if !force && r.State != StateSubmitted {
		return nil, ErrInvalidState
	}
	version := Version{
		Version:     r.Draft.Version,
		Content:     r.Draft.Content,
		Author:      r.Draft.Author,
		PublishedAt: time.Now(),
		Labels:      cloneStrings(r.Draft.Labels),
		BizTags:     cloneStrings(r.Draft.BizTags),
		Description: r.Draft.Description,
		Metadata:    cloneMetadata(r.Draft.Metadata),
		MD5:         md5Hex(r.Draft.Content),
	}
	r.Versions = append(r.Versions, version)
	r.CurrentVersion = version.Version
	r.Draft = nil
	r.State = StatePublished
	r.UpdatedAt = version.PublishedAt
	return r, nil
}

// redraft reopens a draft on a published resource, creating a new version
// number based on the current version.
func (l lifecycleOps) redraft(id, content, author string) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	r, ok := l.store.resources[id]
	if !ok {
		return nil, ErrResourceNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Draft != nil {
		return nil, ErrDraftExists
	}
	r.Draft = &Draft{
		Version:   nextVersion(r),
		Content:   content,
		Author:    author,
		UpdatedAt: time.Now(),
	}
	r.State = StateDraft
	r.UpdatedAt = r.Draft.UpdatedAt
	return r, nil
}

// online transitions a PUBLISHED resource to ONLINE.
func (l lifecycleOps) online(id string) (*Resource, error) {
	return l.transition(id, StatePublished, StateOnline)
}

// offline transitions an ONLINE resource to OFFLINE.
func (l lifecycleOps) offline(id string) (*Resource, error) {
	return l.transition(id, StateOnline, StateOffline)
}

// transition moves a resource between two states, enforcing the required
// source state.
func (l lifecycleOps) transition(id string, from, to ResourceState) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	r, ok := l.store.resources[id]
	if !ok {
		return nil, ErrResourceNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.State != from {
		return nil, ErrInvalidState
	}
	r.State = to
	r.UpdatedAt = time.Now()
	return r, nil
}

// requireDraft fetches a resource that must have an open draft.
func (l lifecycleOps) requireDraft(id string) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	l.store.mu.RLock()
	r, ok := l.store.resources[id]
	l.store.mu.RUnlock()
	if !ok {
		return nil, ErrResourceNotFound
	}
	return r, nil
}

// updateLabels replaces the label set on a resource.
func (l lifecycleOps) updateLabels(id string, labels []string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Labels = cloneStrings(labels)
	r.UpdatedAt = time.Now()
	return r, nil
}

// bindLabel adds a single label to the resource.
func (l lifecycleOps) bindLabel(id, label string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !containsString(r.Labels, label) {
		r.Labels = append(r.Labels, label)
	}
	r.UpdatedAt = time.Now()
	return r, nil
}

// unbindLabel removes a single label from the resource.
func (l lifecycleOps) unbindLabel(id, label string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, v := range r.Labels {
		if containsString([]string{v}, label) {
			r.Labels = append(r.Labels[:i], r.Labels[i+1:]...)
			break
		}
	}
	r.UpdatedAt = time.Now()
	return r, nil
}

// updateBizTags replaces the biz tag set on a resource.
func (l lifecycleOps) updateBizTags(id string, tags []string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.BizTags = cloneStrings(tags)
	r.UpdatedAt = time.Now()
	return r, nil
}

// updateScope replaces the scope string on a resource.
func (l lifecycleOps) updateScope(id, scope string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Scope = scope
	r.UpdatedAt = time.Now()
	return r, nil
}

// updateDescription replaces the description on a resource.
func (l lifecycleOps) updateDescription(id, description string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Description = description
	r.UpdatedAt = time.Now()
	return r, nil
}

// updateMetadata merges metadata into a resource.
func (l lifecycleOps) updateMetadata(id string, metadata map[string]string) (*Resource, error) {
	r, err := l.requireResource(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Metadata == nil {
		r.Metadata = map[string]string{}
	}
	for k, v := range metadata {
		r.Metadata[k] = v
	}
	r.UpdatedAt = time.Now()
	return r, nil
}

// requireResource fetches a resource by ID with the store lock held.
func (l lifecycleOps) requireResource(id string) (*Resource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrMissingID
	}
	l.store.mu.RLock()
	r, ok := l.store.resources[id]
	l.store.mu.RUnlock()
	if !ok {
		return nil, ErrResourceNotFound
	}
	return r, nil
}

// nextVersion returns the next version string for a resource. Versions are
// sequential: v1, v2, v3, ...
func nextVersion(r *Resource) string {
	return versionName(len(r.Versions) + 1)
}

// lastState returns the state a resource should revert to when a draft is
// deleted without being published.
func lastState(r *Resource) ResourceState {
	if len(r.Versions) > 0 {
		return StatePublished
	}
	return StateDraft
}

func versionName(n int) string {
	// Simple v1, v2, ... format. Avoids fmt.Sprintf to keep the zero-import
	// contract for this helper.
	if n <= 0 {
		n = 1
	}
	return "v" + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
