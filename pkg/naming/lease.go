// Package naming - lease tracker for ephemeral instance heartbeats.
//
// The tracker runs a single sweep goroutine that ticks at leaseInterval / 2.
// On each tick it collects all leases whose LastBeat is older than
// leaseInterval and calls expireFn with their IDs. This mirrors the Nacos
// Distro heartbeat expiry semantics without the full raft integration.
package naming

import (
	"sync"
	"time"
)

// leaseRecord captures the identity of an ephemeral instance for expiry
// callbacks. The ID matches the Instance.InstanceID stored on the service.
type leaseRecord struct {
	ID          string
	NamespaceID string
	GroupName   string
	ServiceName string
	ClusterName string
	LastBeat    time.Time
}

// leaseTracker owns the ephemeral lease map and a sweep goroutine.
type leaseTracker struct {
	mu       sync.Mutex
	leases   map[string]*leaseRecord
	interval time.Duration
	stopc    chan struct{}
	expires  func(ids []string)
	running  bool
}

func newLeaseTracker(interval time.Duration) *leaseTracker {
	return &leaseTracker{
		leases:   map[string]*leaseRecord{},
		interval: interval,
		stopc:    make(chan struct{}),
	}
}

func (t *leaseTracker) start(expire func(ids []string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return
	}
	t.expires = expire
	t.running = true
	go t.loop()
}

func (t *leaseTracker) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	close(t.stopc)
	t.running = false
}

func (t *leaseTracker) track(id, namespaceID, groupName, serviceName, clusterName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.leases[id] = &leaseRecord{
		ID:          id,
		NamespaceID: namespaceID,
		GroupName:   groupName,
		ServiceName: serviceName,
		ClusterName: clusterName,
		LastBeat:    time.Now(),
	}
}

func (t *leaseTracker) refresh(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rec, ok := t.leases[id]; ok {
		rec.LastBeat = time.Now()
	}
}

func (t *leaseTracker) remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.leases, id)
}

func (t *leaseTracker) lookup(id string) *leaseRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rec, ok := t.leases[id]; ok {
		c := *rec
		return &c
	}
	return nil
}

// loop runs the sweep until stop is called. Uses an interval half the lease
// window so the worst-case detection latency is ~1.5 intervals.
func (t *leaseTracker) loop() {
	tick := t.interval / 2
	if tick < time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopc:
			return
		case <-ticker.C:
			t.sweep()
		}
	}
}

func (t *leaseTracker) sweep() {
	t.mu.Lock()
	now := time.Now()
	var expired []string
	for id, rec := range t.leases {
		if now.Sub(rec.LastBeat) > t.interval {
			expired = append(expired, id)
		}
	}
	t.mu.Unlock()
	if len(expired) > 0 && t.expires != nil {
		t.expires(expired)
	}
}
