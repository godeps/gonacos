// Package naming - selector types and resolution.
package naming

const (
	// SelectorRandom is the default Nacos selector type.
	SelectorRandom = "random"
	// SelectorWeight is the weight-based selector type.
	SelectorWeight = "weight"
	// SelectorMetadata is the metadata-matching selector type.
	SelectorMetadata = "metadata"
	// SelectorConsistencyHash is the consistent-hash selector type.
	SelectorConsistencyHash = "consistencyHash"
)

// SelectorTypes returns the Nacos-compatible selector type list.
func SelectorTypes() []map[string]any {
	return []map[string]any{
		{"type": SelectorRandom, "name": "Random"},
		{"type": SelectorWeight, "name": "Weight"},
		{"type": SelectorMetadata, "name": "Metadata"},
		{"type": SelectorConsistencyHash, "name": "ConsistencyHash"},
	}
}

// normalizeSelector defaults an empty selector to the random type.
func normalizeSelector(s Selector) Selector {
	if s.Type == "" {
		s.Type = SelectorRandom
	}
	if s.Attributes == nil {
		s.Attributes = map[string]string{}
	}
	return s
}
