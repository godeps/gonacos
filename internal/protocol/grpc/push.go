package grpc

import (
	"sync"
)

// ConnectionRegistry tracks active BiRequestStream connections by their
// connection ID so the server can push notifications to specific clients.
// The SDK obtains a connection ID from the ServerCheck RPC and includes it
// in the ConnectionSetupRequest headers when opening the BiRequestStream.
type ConnectionRegistry struct {
	mu      sync.RWMutex
	senders map[string]func(Payload) error
}

// NewConnectionRegistry returns an empty registry.
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{senders: map[string]func(Payload) error{}}
}

// Register associates a connection ID with its send function. The send
// function writes a Payload frame on the BiRequestStream. Calling Register
// with an existing ID replaces the previous sender.
func (r *ConnectionRegistry) Register(connID string, send func(Payload) error) {
	if connID == "" || send == nil {
		return
	}
	r.mu.Lock()
	r.senders[connID] = send
	r.mu.Unlock()
}

// Unregister removes a connection. Safe to call multiple times.
func (r *ConnectionRegistry) Unregister(connID string) {
	if connID == "" {
		return
	}
	r.mu.Lock()
	delete(r.senders, connID)
	r.mu.Unlock()
}

// Push sends a payload to a specific connection. Returns false if the
// connection is not registered or the send fails.
func (r *ConnectionRegistry) Push(connID string, payload Payload) bool {
	r.mu.RLock()
	send, ok := r.senders[connID]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	return send(payload) == nil
}

// Has reports whether a connection is currently registered.
func (r *ConnectionRegistry) Has(connID string) bool {
	if connID == "" {
		return false
	}
	r.mu.RLock()
	_, ok := r.senders[connID]
	r.mu.RUnlock()
	return ok
}

// Count returns the number of active connections.
func (r *ConnectionRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.senders)
}
