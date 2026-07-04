package config

import (
	"strings"
	"sync"
	"time"
)

const (
	// listenerTTL is how long a listener entry remains valid without a
	// heartbeat. SDK clients poll config every 30s by default, so 5 minutes
	// gives ample margin.
	listenerTTL = 5 * time.Minute
)

// listenerKey identifies a unique listener relationship between a client IP and
// a config item.
type listenerKey struct {
	namespaceID string
	groupName   string
	dataID      string
	ip          string
}

// listenerEntry records the last known md5 the client has acknowledged and the
// last time we heard from it.
type listenerEntry struct {
	md5      string
	lastSeen time.Time
}

// listenerRegistry tracks which clients are listening to which configs. The
// data feeds the /v3/admin/cs/listener and /v3/admin/cs/config/listener
// endpoints so operators can see who is subscribed to a given config and what
// md5 each client currently holds.
type listenerRegistry struct {
	mu       sync.Mutex
	entries  map[listenerKey]listenerEntry
	stopOnce sync.Once
	stopCh   chan struct{}
}

func newListenerRegistry() *listenerRegistry {
	r := &listenerRegistry{
		entries: map[listenerKey]listenerEntry{},
		stopCh:  make(chan struct{}),
	}
	go r.gcLoop()
	return r
}

// track records or refreshes a listener. Called from the client query path
// (HTTP and gRPC) every time a client polls a config.
func (r *listenerRegistry) track(ip, namespaceID, groupName, dataID, md5 string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	k := listenerKey{
		namespaceID: normalizeNamespace(namespaceID),
		groupName:   strings.TrimSpace(groupName),
		dataID:      strings.TrimSpace(dataID),
		ip:          ip,
	}
	if k.groupName == "" || k.dataID == "" {
		return
	}
	r.mu.Lock()
	r.entries[k] = listenerEntry{md5: md5, lastSeen: time.Now()}
	r.mu.Unlock()
}

// remove deletes a listener entry, called when a client deregisters.
func (r *listenerRegistry) remove(ip, namespaceID, groupName, dataID string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	k := listenerKey{
		namespaceID: normalizeNamespace(namespaceID),
		groupName:   strings.TrimSpace(groupName),
		dataID:      strings.TrimSpace(dataID),
		ip:          ip,
	}
	r.mu.Lock()
	delete(r.entries, k)
	r.mu.Unlock()
}

// byConfig returns a map of ip -> md5 for all live listeners of the given
// config item.
func (r *listenerRegistry) byConfig(namespaceID, groupName, dataID string) map[string]string {
	namespaceID = normalizeNamespace(namespaceID)
	groupName = strings.TrimSpace(groupName)
	dataID = strings.TrimSpace(dataID)
	out := map[string]string{}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.entries {
		if k.namespaceID != namespaceID || k.groupName != groupName || k.dataID != dataID {
			continue
		}
		if now.Sub(v.lastSeen) > listenerTTL {
			continue
		}
		out[k.ip] = v.md5
	}
	return out
}

// byIP returns a map of "groupName dataID" -> md5 for all configs a given IP
// is listening to. If namespaceID is non-empty, results are filtered to that
// namespace.
func (r *listenerRegistry) byIP(ip, namespaceID string) map[string]string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return map[string]string{}
	}
	namespaceID = strings.TrimSpace(namespaceID)
	out := map[string]string{}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.entries {
		if k.ip != ip {
			continue
		}
		if namespaceID != "" && k.namespaceID != normalizeNamespace(namespaceID) {
			continue
		}
		if now.Sub(v.lastSeen) > listenerTTL {
			continue
		}
		out[k.groupName+" "+k.dataID] = v.md5
	}
	return out
}

// stop halts the background GC goroutine.
func (r *listenerRegistry) stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}

func (r *listenerRegistry) gcLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.prune()
		}
	}
}

func (r *listenerRegistry) prune() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.entries {
		if now.Sub(v.lastSeen) > listenerTTL {
			delete(r.entries, k)
		}
	}
}
