package ai

import (
	"sort"
	"time"

	"github.com/godeps/gonacos/pkg/ai/apitomcp"
	"github.com/godeps/gonacos/pkg/ai/mcptemplate"
)

// aiResourceSnap captures a resource with draft and version content included
// (those fields are json:"-" in the live type because they are served via
// dedicated download endpoints).
type aiResourceSnap struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	State          string            `json:"state"`
	Owner          string            `json:"owner"`
	Description    string            `json:"description"`
	Labels         []string          `json:"labels"`
	BizTags        []string          `json:"bizTags"`
	Scope          string            `json:"scope"`
	Metadata       map[string]string `json:"metadata"`
	Draft          *aiDraftSnap      `json:"draft"`
	Versions       []aiVersionSnap   `json:"versions"`
	CurrentVersion string            `json:"currentVersion"`
	CreatedAt      int64             `json:"createdAt"`
	UpdatedAt      int64             `json:"updatedAt"`
}

type aiDraftSnap struct {
	Version     string            `json:"version"`
	Content     string            `json:"content"`
	Author      string            `json:"author"`
	Labels      []string          `json:"labels"`
	BizTags     []string          `json:"bizTags"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
	UpdatedAt   int64             `json:"updatedAt"`
}

type aiVersionSnap struct {
	Version     string            `json:"version"`
	Content     string            `json:"content"`
	Author      string            `json:"author"`
	PublishedAt int64             `json:"publishedAt"`
	Labels      []string          `json:"labels"`
	BizTags     []string          `json:"bizTags"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
	MD5         string            `json:"md5"`
}

type aiSnapshot struct {
	Prompts   []aiResourceSnap       `json:"prompts"`
	Skills    []aiResourceSnap       `json:"skills"`
	Specs     []aiResourceSnap       `json:"specs"`
	Mcp       []McpServer            `json:"mcp"`
	A2A       []A2AAgent             `json:"a2a"`
	Pipelines []Pipeline             `json:"pipelines"`
	Apitomcp  []ApitomcpConfig       `json:"apitomcp,omitempty"`
	Templates []mcptemplate.Template `json:"templates,omitempty"`
}

// SnapshotKey identifies the AI service in backup envelopes.
func (s *Service) SnapshotKey() string { return "ai" }

// Snapshot returns all AI registry state. Import sources are excluded because
// they are discovery catalogs rather than user-owned state.
func (s *Service) Snapshot() (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mcpServers := make([]McpServer, 0, len(s.mcp.list()))
	for _, srv := range s.mcp.list() {
		mcpServers = append(mcpServers, *srv)
	}
	a2aAgents := make([]A2AAgent, 0, len(s.a2a.list()))
	for _, agent := range s.a2a.list() {
		a2aAgents = append(a2aAgents, *agent)
	}
	snap := aiSnapshot{
		Prompts:   snapshotResources(s.prompts),
		Skills:    snapshotResources(s.skills),
		Specs:     snapshotResources(s.specs),
		Mcp:       mcpServers,
		A2A:       a2aAgents,
		Pipelines: pipelineListToValues(s.pipelines.list()),
		Apitomcp:  apitomcpListToValues(s.apitomcp.list()),
		Templates: templateListToValues(s.templates.list()),
	}
	sort.Slice(snap.Mcp, func(i, j int) bool { return snap.Mcp[i].ID < snap.Mcp[j].ID })
	sort.Slice(snap.A2A, func(i, j int) bool { return snap.A2A[i].ID < snap.A2A[j].ID })
	return snap, nil
}

func snapshotResources(store *resourceStore) []aiResourceSnap {
	if store == nil {
		return nil
	}
	list := store.list()
	out := make([]aiResourceSnap, 0, len(list))
	for _, r := range list {
		out = append(out, snapshotResource(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func snapshotResource(r *Resource) aiResourceSnap {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap := aiResourceSnap{
		ID:             r.ID,
		Name:           r.Name,
		Type:           r.Type,
		State:          string(r.State),
		Owner:          r.Owner,
		Description:    r.Description,
		Labels:         append([]string(nil), r.Labels...),
		BizTags:        append([]string(nil), r.BizTags...),
		Scope:          r.Scope,
		Metadata:       copyStringMap(r.Metadata),
		CurrentVersion: r.CurrentVersion,
	}
	if !r.CreatedAt.IsZero() {
		snap.CreatedAt = r.CreatedAt.UnixMilli()
	}
	if !r.UpdatedAt.IsZero() {
		snap.UpdatedAt = r.UpdatedAt.UnixMilli()
	}
	if r.Draft != nil {
		snap.Draft = &aiDraftSnap{
			Version:     r.Draft.Version,
			Content:     r.Draft.Content,
			Author:      r.Draft.Author,
			Labels:      append([]string(nil), r.Draft.Labels...),
			BizTags:     append([]string(nil), r.Draft.BizTags...),
			Description: r.Draft.Description,
			Metadata:    copyStringMap(r.Draft.Metadata),
		}
		if !r.Draft.UpdatedAt.IsZero() {
			snap.Draft.UpdatedAt = r.Draft.UpdatedAt.UnixMilli()
		}
	}
	for _, v := range r.Versions {
		snap.Versions = append(snap.Versions, aiVersionSnap{
			Version:     v.Version,
			Content:     v.Content,
			Author:      v.Author,
			PublishedAt: tsMilli(v.PublishedAt),
			Labels:      append([]string(nil), v.Labels...),
			BizTags:     append([]string(nil), v.BizTags...),
			Description: v.Description,
			Metadata:    copyStringMap(v.Metadata),
			MD5:         v.MD5,
		})
	}
	return snap
}

// Restore replaces all AI registry state.
func (s *Service) Restore(data any) error {
	snap, ok := data.(map[string]any)
	if !ok {
		return errAISnapshotShape
	}
	prompts, err := decodeAIResources(snap["prompts"])
	if err != nil {
		return err
	}
	skills, err := decodeAIResources(snap["skills"])
	if err != nil {
		return err
	}
	specs, err := decodeAIResources(snap["specs"])
	if err != nil {
		return err
	}
	mcp, err := decodeAIMcp(snap["mcp"])
	if err != nil {
		return err
	}
	a2a, err := decodeAIA2A(snap["a2a"])
	if err != nil {
		return err
	}
	pipelines, err := decodeAIPipelines(snap["pipelines"])
	if err != nil {
		return err
	}
	apitomcpCfgs, err := decodeApitomcpConfigs(snap["apitomcp"])
	if err != nil {
		return err
	}
	userTemplates, err := decodeUserTemplates(snap["templates"])
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	restoreResources(s.prompts, prompts)
	restoreResources(s.skills, skills)
	restoreResources(s.specs, specs)
	s.mcp.mu.Lock()
	s.mcp.servers = map[string]*McpServer{}
	for _, srv := range mcp {
		c := srv
		s.mcp.servers[srv.ID] = &c
	}
	s.mcp.mu.Unlock()
	s.a2a.mu.Lock()
	s.a2a.agents = map[string]*A2AAgent{}
	for _, agent := range a2a {
		c := agent
		s.a2a.agents[agent.ID] = &c
	}
	s.a2a.mu.Unlock()
	if pipelines != nil {
		s.pipelines.replace(pipelines)
	}
	if apitomcpCfgs != nil {
		s.apitomcp.replace(apitomcpCfgs)
		// Remount backends on the router (if attached).
		for _, cfg := range apitomcpCfgs {
			conv := apitomcp.NewConverterFromEnv()
			if parsed, err := conv.LoadYAML([]byte(cfg.YAML)); err == nil {
				s.mountApitomcpBackend(parsed)
			}
		}
	}
	if userTemplates != nil {
		s.templates.replace(userTemplates)
	}
	return nil
}

func pipelineListToValues(list []*Pipeline) []Pipeline {
	out := make([]Pipeline, len(list))
	for i, p := range list {
		out[i] = *p
	}
	return out
}

func apitomcpListToValues(list []*ApitomcpConfig) []ApitomcpConfig {
	out := make([]ApitomcpConfig, len(list))
	for i, c := range list {
		out[i] = *c
	}
	return out
}

func templateListToValues(list []*UserTemplate) []mcptemplate.Template {
	out := make([]mcptemplate.Template, len(list))
	for i, t := range list {
		out[i] = t.Template
	}
	return out
}

func decodeApitomcpConfigs(raw any) ([]ApitomcpConfig, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	out := make([]ApitomcpConfig, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAISnapshotShape
		}
		cfg := ApitomcpConfig{
			Name:        getString(m, "name"),
			Description: getString(m, "description"),
			YAML:        getString(m, "yaml"),
			ServerName:  getString(m, "serverName"),
			ToolCount:   int(getInt64(m, "toolCount")),
		}
		if ts := getInt64(m, "createdAt"); ts > 0 {
			cfg.CreatedAt = time.UnixMilli(ts)
		}
		if ts := getInt64(m, "updatedAt"); ts > 0 {
			cfg.UpdatedAt = time.UnixMilli(ts)
		}
		out = append(out, cfg)
	}
	return out, nil
}

func decodeUserTemplates(raw any) ([]UserTemplate, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	out := make([]UserTemplate, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAISnapshotShape
		}
		t := UserTemplate{
			Template: mcptemplate.Template{
				ID:          getString(m, "id"),
				Name:        getString(m, "name"),
				Description: getString(m, "description"),
				Category:    getString(m, "category"),
				Body:        getString(m, "body"),
			},
		}
		if ts := getInt64(m, "createdAt"); ts > 0 {
			t.CreatedAt = time.UnixMilli(ts)
		}
		if ts := getInt64(m, "updatedAt"); ts > 0 {
			t.UpdatedAt = time.UnixMilli(ts)
		}
		if vars, ok := m["variables"].([]any); ok {
			for _, v := range vars {
				vm, ok := v.(map[string]any)
				if !ok {
					continue
				}
				t.Variables = append(t.Variables, mcptemplate.Variable{
					Name:        getString(vm, "name"),
					Description: getString(vm, "description"),
					Default:     getString(vm, "default"),
					Required:    getBool(vm, "required"),
				})
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func restoreResources(store *resourceStore, list []aiResourceSnap) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.resources = map[string]*Resource{}
	for _, snap := range list {
		store.resources[snap.ID] = restoreResource(snap)
	}
}

func restoreResource(snap aiResourceSnap) *Resource {
	r := &Resource{
		ID:             snap.ID,
		Name:           snap.Name,
		Type:           snap.Type,
		State:          ResourceState(snap.State),
		Owner:          snap.Owner,
		Description:    snap.Description,
		Labels:         append([]string(nil), snap.Labels...),
		BizTags:        append([]string(nil), snap.BizTags...),
		Scope:          snap.Scope,
		Metadata:       copyStringMap(snap.Metadata),
		CurrentVersion: snap.CurrentVersion,
	}
	if snap.CreatedAt > 0 {
		r.CreatedAt = time.UnixMilli(snap.CreatedAt)
	}
	if snap.UpdatedAt > 0 {
		r.UpdatedAt = time.UnixMilli(snap.UpdatedAt)
	}
	if snap.Draft != nil {
		r.Draft = &Draft{
			Version:     snap.Draft.Version,
			Content:     snap.Draft.Content,
			Author:      snap.Draft.Author,
			Labels:      append([]string(nil), snap.Draft.Labels...),
			BizTags:     append([]string(nil), snap.Draft.BizTags...),
			Description: snap.Draft.Description,
			Metadata:    copyStringMap(snap.Draft.Metadata),
		}
		if snap.Draft.UpdatedAt > 0 {
			r.Draft.UpdatedAt = time.UnixMilli(snap.Draft.UpdatedAt)
		}
	}
	for _, v := range snap.Versions {
		r.Versions = append(r.Versions, Version{
			Version:     v.Version,
			Content:     v.Content,
			Author:      v.Author,
			PublishedAt: time.UnixMilli(v.PublishedAt),
			Labels:      append([]string(nil), v.Labels...),
			BizTags:     append([]string(nil), v.BizTags...),
			Description: v.Description,
			Metadata:    copyStringMap(v.Metadata),
			MD5:         v.MD5,
		})
	}
	return r
}

func decodeAIResources(raw any) ([]aiResourceSnap, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errAISnapshotShape
	}
	out := make([]aiResourceSnap, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAISnapshotShape
		}
		snap := aiResourceSnap{
			ID:             getString(m, "id"),
			Name:           getString(m, "name"),
			Type:           getString(m, "type"),
			State:          getString(m, "state"),
			Owner:          getString(m, "owner"),
			Description:    getString(m, "description"),
			Labels:         getStringList(m, "labels"),
			BizTags:        getStringList(m, "bizTags"),
			Scope:          getString(m, "scope"),
			Metadata:       getStringMap(m, "metadata"),
			CurrentVersion: getString(m, "currentVersion"),
			CreatedAt:      getInt64(m, "createdAt"),
			UpdatedAt:      getInt64(m, "updatedAt"),
		}
		if d, ok := m["draft"].(map[string]any); ok {
			snap.Draft = &aiDraftSnap{
				Version:     getString(d, "version"),
				Content:     getString(d, "content"),
				Author:      getString(d, "author"),
				Labels:      getStringList(d, "labels"),
				BizTags:     getStringList(d, "bizTags"),
				Description: getString(d, "description"),
				Metadata:    getStringMap(d, "metadata"),
				UpdatedAt:   getInt64(d, "updatedAt"),
			}
		}
		if vs, ok := m["versions"].([]any); ok {
			for _, v := range vs {
				vm, ok := v.(map[string]any)
				if !ok {
					continue
				}
				snap.Versions = append(snap.Versions, aiVersionSnap{
					Version:     getString(vm, "version"),
					Content:     getString(vm, "content"),
					Author:      getString(vm, "author"),
					PublishedAt: getInt64(vm, "publishedAt"),
					Labels:      getStringList(vm, "labels"),
					BizTags:     getStringList(vm, "bizTags"),
					Description: getString(vm, "description"),
					Metadata:    getStringMap(vm, "metadata"),
					MD5:         getString(vm, "md5"),
				})
			}
		}
		out = append(out, snap)
	}
	return out, nil
}

func decodeAIMcp(raw any) ([]McpServer, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errAISnapshotShape
	}
	out := make([]McpServer, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAISnapshotShape
		}
		srv := McpServer{
			ID:          getString(m, "id"),
			Name:        getString(m, "name"),
			Description: getString(m, "description"),
			Protocol:    getString(m, "protocol"),
			Endpoint:    getString(m, "endpoint"),
			Labels:      getStringList(m, "labels"),
			Metadata:    getStringMap(m, "metadata"),
		}
		if ts := getInt64(m, "createdAt"); ts > 0 {
			srv.CreatedAt = time.UnixMilli(ts)
		}
		if ts := getInt64(m, "updatedAt"); ts > 0 {
			srv.UpdatedAt = time.UnixMilli(ts)
		}
		if tools, ok := m["tools"].([]any); ok {
			for _, t := range tools {
				tm, ok := t.(map[string]any)
				if !ok {
					continue
				}
				tool := McpTool{
					Name:        getString(tm, "name"),
					Description: getString(tm, "description"),
					InputSchema: getStringAnyMap(tm, "inputSchema"),
					Metadata:    getStringMap(tm, "metadata"),
				}
				srv.Tools = append(srv.Tools, tool)
			}
		}
		out = append(out, srv)
	}
	return out, nil
}

func decodeAIA2A(raw any) ([]A2AAgent, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errAISnapshotShape
	}
	out := make([]A2AAgent, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAISnapshotShape
		}
		agent := A2AAgent{
			ID:           getString(m, "id"),
			Name:         getString(m, "name"),
			Description:  getString(m, "description"),
			Endpoint:     getString(m, "endpoint"),
			Protocol:     getString(m, "protocol"),
			Capabilities: getStringList(m, "capabilities"),
			Metadata:     getStringMap(m, "metadata"),
			Version:      getString(m, "version"),
		}
		if ts := getInt64(m, "createdAt"); ts > 0 {
			agent.CreatedAt = time.UnixMilli(ts)
		}
		if ts := getInt64(m, "updatedAt"); ts > 0 {
			agent.UpdatedAt = time.UnixMilli(ts)
		}
		if vs, ok := m["versions"].([]any); ok {
			for _, v := range vs {
				vm, ok := v.(map[string]any)
				if !ok {
					continue
				}
				ver := A2AAgentVersion{
					Version: getString(vm, "version"),
					Author:  getString(vm, "author"),
				}
				if ts := getInt64(vm, "updatedAt"); ts > 0 {
					ver.UpdatedAt = time.UnixMilli(ts)
				}
				agent.Versions = append(agent.Versions, ver)
			}
		}
		out = append(out, agent)
	}
	return out, nil
}

func decodeAIPipelines(raw any) ([]Pipeline, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errAISnapshotShape
	}
	out := make([]Pipeline, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAISnapshotShape
		}
		p := Pipeline{
			ID:          getString(m, "pipelineId"),
			Name:        getString(m, "name"),
			Description: getString(m, "description"),
			Metadata:    getStringMap(m, "metadata"),
		}
		if stages, ok := m["stages"].([]any); ok {
			for _, s := range stages {
				sm, ok := s.(map[string]any)
				if !ok {
					continue
				}
				p.Stages = append(p.Stages, PipelineStage{
					Name:   getString(sm, "name"),
					Type:   getString(sm, "type"),
					Config: getStringMap(sm, "config"),
				})
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func tsMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getStringList(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func getStringMap(m map[string]any, key string) map[string]string {
	raw, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func getStringAnyMap(m map[string]any, key string) map[string]any {
	raw, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	return out
}

func getInt64(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

var errAISnapshotShape = snapshotShapeError("ai snapshot shape mismatch")

type snapshotShapeError string

func (e snapshotShapeError) Error() string { return string(e) }
