package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Snapshotter captures a service's in-memory state as JSON-serializable data
// and restores it from the same shape. Services implement this so the backup
// coordinator can produce a unified point-in-time dump.
type Snapshotter interface {
	// SnapshotKey returns the top-level key under which the service's state
	// is stored in the backup envelope (e.g. "namespace", "config").
	SnapshotKey() string
	// Snapshot returns a JSON-serializable representation of the service state.
	Snapshot() (any, error)
	// Restore replaces the service state from the given decoded value.
	// Implementations must clear existing state before loading.
	Restore(data any) error
}

// Envelope is the top-level backup structure written to disk or returned by
// the backup HTTP endpoint. It carries a version, timestamp, and per-service
// state keyed by SnapshotKey.
//
// Checksum is a SHA-256 hex digest of the marshaled Services map, computed
// by [Coordinator.Snapshot] and verified by [Coordinator.Restore]. It detects
// disk corruption (bit rot, partial writes that survived atomic rename on a
// network filesystem) and accidental tampering with the dump file. Empty
// Checksum (legacy snapshots written before this field existed) skips
// verification so old dumps continue to load. It is NOT an authentication
// tag — an adversary with write access to the dump file can recompute the
// checksum after modifying Services. Use HMAC with a secret key for
// authenticated integrity if that threat model applies.
type Envelope struct {
	Version   string         `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	Checksum  string         `json:"checksum,omitempty"`
	Services  map[string]any `json:"services"`
}

const EnvelopeVersion = "gonacos/v1"

// Coordinator orchestrates snapshot and restore across all registered
// services. Registration order is stable (sorted by key) so backups are
// deterministic.
type Coordinator struct {
	mu       sync.Mutex
	services map[string]Snapshotter
}

func NewCoordinator() *Coordinator {
	return &Coordinator{services: make(map[string]Snapshotter)}
}

// Register adds a snapshotter. Duplicate keys replace the previous entry.
func (c *Coordinator) Register(s Snapshotter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services[s.SnapshotKey()] = s
}

// Snapshot produces a backup envelope. Errors from individual services are
// collected; the envelope still contains successful entries. Returns an error
// only if no service could be snapshotted.
func (c *Coordinator) Snapshot() (*Envelope, error) {
	c.mu.Lock()
	keys := make([]string, 0, len(c.services))
	for k := range c.services {
		keys = append(keys, k)
	}
	c.mu.Unlock()
	sort.Strings(keys)

	env := &Envelope{
		Version:   EnvelopeVersion,
		CreatedAt: time.Now().UTC(),
		Services:  make(map[string]any, len(keys)),
	}
	var errs []string
	for _, k := range keys {
		c.mu.Lock()
		s := c.services[k]
		c.mu.Unlock()
		data, err := s.Snapshot()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", k, err))
			continue
		}
		env.Services[k] = data
	}
	if len(env.Services) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("snapshot failed for all services: %v", errs)
	}
	// Normalize Services to JSON-native form (maps with sorted keys instead
	// of Go structs with declaration-order fields) before computing the
	// checksum. Without this, the checksum computed in Snapshot (on structs)
	// would differ from the checksum computed in Restore (on maps unmarshaled
	// from JSON) — struct field order ≠ sorted map key order. Normalizing
	// here means both sides hash the same bytes for the same logical state.
	raw, err := json.Marshal(env.Services)
	if err != nil {
		return nil, fmt.Errorf("normalize services: %w", err)
	}
	env.Services = make(map[string]any, len(keys))
	if err := json.Unmarshal(raw, &env.Services); err != nil {
		return nil, fmt.Errorf("denormalize services: %w", err)
	}
	checksum, err := computeChecksum(env.Services)
	if err != nil {
		return nil, fmt.Errorf("compute checksum: %w", err)
	}
	env.Checksum = checksum
	return env, nil
}

// Restore loads an envelope and replays it into the registered services.
// Unknown keys are skipped. Restore order follows the envelope's key order;
// services should be order-independent (they clear state before loading).
//
// When env.Checksum is set, it is verified against a freshly computed digest
// of env.Services before any service is restored. A mismatch aborts the
// restore so corrupted state does not silently overwrite in-memory data —
// the operator can then fall back to a prior snapshot.N.json backup. Empty
// Checksum (legacy snapshots) skips verification for backward compatibility.
func (c *Coordinator) Restore(env *Envelope) error {
	if env == nil {
		return fmt.Errorf("nil envelope")
	}
	if env.Version == "" {
		return fmt.Errorf("missing envelope version")
	}
	if env.Services == nil {
		return fmt.Errorf("missing services map")
	}
	if env.Checksum != "" {
		got, err := computeChecksum(env.Services)
		if err != nil {
			return fmt.Errorf("compute checksum for verification: %w", err)
		}
		if got != env.Checksum {
			return fmt.Errorf("checksum mismatch: envelope says %s but services digest to %s — snapshot is corrupted or was modified after writing", env.Checksum, got)
		}
	}
	keys := make([]string, 0, len(env.Services))
	for k := range env.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []string
	for _, k := range keys {
		s, ok := c.services[k]
		if !ok {
			continue
		}
		if err := s.Restore(env.Services[k]); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", k, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("restore errors: %v", errs)
	}
	return nil
}

// computeChecksum returns the SHA-256 hex digest of the JSON-marshaled
// services map. Go's encoding/json sorts map keys at every level, so the
// output is deterministic for the same logical state regardless of map
// iteration order — a snapshot of the same data always produces the same
// digest.
func computeChecksum(services map[string]any) (string, error) {
	if len(services) == 0 {
		return "", nil
	}
	data, err := json.Marshal(services)
	if err != nil {
		return "", fmt.Errorf("marshal services: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
