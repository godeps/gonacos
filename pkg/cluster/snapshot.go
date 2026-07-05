package cluster

import (
	"sort"
	"time"
)

// clusterSnapshot captures members, plugins, and log level. Mode is owned by
// the constructor and not restored.
type clusterSnapshot struct {
	Members  []memberSnap `json:"members"`
	Plugins  []pluginSnap `json:"plugins"`
	LogLevel string       `json:"logLevel"`
}

type memberSnap struct {
	ID          string            `json:"id"`
	IP          string            `json:"ip"`
	Port        int               `json:"port"`
	State       string            `json:"state"`
	APIPort     int               `json:"apiPort"`
	GRPCPort    int               `json:"grpcPort"`
	RaftPort    int               `json:"raftPort"`
	GrpcAPIInfo string            `json:"grpcApiInfo"`
	Metadata    map[string]string `json:"metadata"`
	Abilities   []string          `json:"abilities"`
	IsSelf      bool              `json:"isSelf"`
	UpdatedAt   int64             `json:"updatedAt"`
}

type pluginSnap struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        string            `json:"type"`
	Available   bool              `json:"available"`
	Enabled     bool              `json:"enabled"`
	Config      map[string]string `json:"config"`
	Status      map[string]string `json:"status"`
	UpdatedAt   int64             `json:"updatedAt"`
}

// SnapshotKey identifies the cluster service in backup envelopes.
func (s *Service) SnapshotKey() string { return "cluster" }

// Snapshot returns members, plugins, and log level.
func (s *Service) Snapshot() (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := clusterSnapshot{LogLevel: s.logLevel}
	for _, m := range s.members {
		snap.Members = append(snap.Members, snapshotMember(m))
	}
	sort.Slice(snap.Members, func(i, j int) bool { return snap.Members[i].ID < snap.Members[j].ID })
	for _, p := range s.plugins {
		snap.Plugins = append(snap.Plugins, snapshotPlugin(p))
	}
	sort.Slice(snap.Plugins, func(i, j int) bool { return snap.Plugins[i].ID < snap.Plugins[j].ID })
	return snap, nil
}

func snapshotMember(m *Member) memberSnap {
	out := memberSnap{
		ID:          m.ID,
		IP:          m.IP,
		Port:        m.Port,
		State:       m.State,
		APIPort:     m.APIPort,
		GRPCPort:    m.GRPCPort,
		RaftPort:    m.RaftPort,
		GrpcAPIInfo: m.GrpcAPIInfo,
		Metadata:    copyStringMap(m.Metadata),
		Abilities:   append([]string(nil), m.Abilities...),
		IsSelf:      m.IsSelf,
	}
	if !m.UpdatedAt.IsZero() {
		out.UpdatedAt = m.UpdatedAt.UnixMilli()
	}
	return out
}

func snapshotPlugin(p *Plugin) pluginSnap {
	out := pluginSnap{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Type:        p.Type,
		Available:   p.Available,
		Enabled:     p.Enabled,
		Config:      copyStringMap(p.Config),
		Status:      copyStringMap(p.Status),
	}
	if !p.UpdatedAt.IsZero() {
		out.UpdatedAt = p.UpdatedAt.UnixMilli()
	}
	return out
}

// Restore replaces members, plugins, and log level. The self member is
// preserved from the running process; backup members are merged in but the
// self entry always reflects the current process identity.
func (s *Service) Restore(data any) error {
	snap, ok := data.(map[string]any)
	if !ok {
		return errClusterSnapshotShape
	}
	members, err := decodeMembers(snap["members"])
	if err != nil {
		return err
	}
	plugins, err := decodePlugins(snap["plugins"])
	if err != nil {
		return err
	}
	logLevel := ""
	if v, ok := snap["logLevel"].(string); ok {
		logLevel = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	selfID := ""
	if s.self != nil {
		selfID = s.self.ID
	}
	s.members = map[string]*Member{}
	for _, m := range members {
		if m.ID == selfID {
			continue
		}
		s.members[m.ID] = m
	}
	if s.self != nil {
		s.members[s.self.ID] = s.self
	}
	s.plugins = map[string]*Plugin{}
	for _, p := range plugins {
		s.plugins[p.ID] = p
	}
	if logLevel != "" {
		s.logLevel = logLevel
	}
	return nil
}

func decodeMembers(raw any) ([]*Member, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errClusterSnapshotShape
	}
	out := make([]*Member, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errClusterSnapshotShape
		}
		member := &Member{
			ID:          getString(m, "id"),
			IP:          getString(m, "ip"),
			Port:        int(getFloat(m, "port")),
			State:       getString(m, "state"),
			APIPort:     int(getFloat(m, "apiPort")),
			GRPCPort:    int(getFloat(m, "grpcPort")),
			RaftPort:    int(getFloat(m, "raftPort")),
			GrpcAPIInfo: getString(m, "grpcApiInfo"),
			Metadata:    getStringMap(m, "metadata"),
			IsSelf:      getBool(m, "isSelf"),
		}
		if raw, ok := m["abilities"].([]any); ok {
			member.Abilities = make([]string, 0, len(raw))
			for _, a := range raw {
				if s, ok := a.(string); ok {
					member.Abilities = append(member.Abilities, s)
				}
			}
		}
		if ts := getFloat(m, "updatedAt"); ts > 0 {
			member.UpdatedAt = time.UnixMilli(int64(ts))
		}
		out = append(out, member)
	}
	return out, nil
}

func decodePlugins(raw any) ([]*Plugin, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errClusterSnapshotShape
	}
	out := make([]*Plugin, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errClusterSnapshotShape
		}
		plugin := &Plugin{
			ID:          getString(m, "id"),
			Name:        getString(m, "name"),
			Description: getString(m, "description"),
			Type:        getString(m, "type"),
			Available:   getBool(m, "available"),
			Enabled:     getBool(m, "enabled"),
			Config:      getStringMap(m, "config"),
			Status:      getStringMap(m, "status"),
		}
		if ts := getFloat(m, "updatedAt"); ts > 0 {
			plugin.UpdatedAt = time.UnixMilli(int64(ts))
		}
		out = append(out, plugin)
	}
	return out, nil
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

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
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

var errClusterSnapshotShape = snapshotShapeError("cluster snapshot shape mismatch")

type snapshotShapeError string

func (e snapshotShapeError) Error() string { return string(e) }
