package namespace

import "sort"

// SnapshotKey identifies the namespace service in backup envelopes.
func (s *Service) SnapshotKey() string { return "namespace" }

// Snapshot returns the current set of namespaces for backup. ConfigCount is
// not preserved because it is derived from config service state at runtime.
func (s *Service) Snapshot() (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Namespace, 0, len(s.namespaces))
	for _, ns := range s.namespaces {
		items = append(items, ns)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Namespace < items[j].Namespace })
	return items, nil
}

// Restore replaces all namespace state from the given list. The public
// namespace is re-seeded if missing so the post-restore state is always
// usable.
func (s *Service) Restore(data any) error {
	items, ok := data.([]any)
	if !ok {
		return errNamespaceSnapshotShape
	}
	parsed := make([]Namespace, 0, len(items))
	for _, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			return errNamespaceSnapshotShape
		}
		ns := Namespace{}
		if v, ok := m["namespace"].(string); ok {
			ns.Namespace = v
		}
		if v, ok := m["namespaceShowName"].(string); ok {
			ns.NamespaceShowName = v
		}
		if v, ok := m["namespaceDesc"].(string); ok {
			ns.NamespaceDesc = v
		}
		if v, ok := m["quota"].(float64); ok {
			ns.Quota = int(v)
		}
		if v, ok := m["type"].(float64); ok {
			ns.Type = int(v)
		}
		parsed = append(parsed, ns)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.namespaces = map[string]Namespace{}
	for _, ns := range parsed {
		if ns.Namespace == "" {
			continue
		}
		s.namespaces[ns.Namespace] = ns
	}
	if _, ok := s.namespaces[PublicID]; !ok {
		s.namespaces[PublicID] = Namespace{
			Namespace:         PublicID,
			NamespaceShowName: PublicName,
			NamespaceDesc:     "Default Namespace",
			Quota:             200,
			ConfigCount:       0,
			Type:              0,
		}
	}
	return nil
}

var errNamespaceSnapshotShape = snapshotShapeError("namespace snapshot must be a list of objects")

type snapshotShapeError string

func (e snapshotShapeError) Error() string { return string(e) }
