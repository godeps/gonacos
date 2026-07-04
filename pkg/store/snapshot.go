package store

import (
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
type Envelope struct {
	Version   string         `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
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
	return env, nil
}

// Restore loads an envelope and replays it into the registered services.
// Unknown keys are skipped. Restore order follows the envelope's key order;
// services should be order-independent (they clear state before loading).
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
