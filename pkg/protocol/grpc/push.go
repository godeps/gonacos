package grpc

import (
	"sync"
)

// ConnectionRegistry tracks active BiRequestStream connections by their
// transport ID so the server can push notifications to specific clients.
// The identity comes from the accepted socket, shared by that socket's
// HTTP/2 streams. It must not be derived from an IP address or client header.
type ConnectionRegistry struct {
	mu           sync.RWMutex
	senders      map[string]*connectionSender
	onUnregister func(string)
}

type connectionSender struct {
	mu     sync.Mutex
	send   func(Payload) error
	closed bool
}

// NewConnectionRegistry returns an empty registry.
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{senders: map[string]*connectionSender{}}
}

// SetOnUnregister installs cleanup for subscriptions owned by a transport.
func (r *ConnectionRegistry) SetOnUnregister(fn func(string)) {
	r.mu.Lock()
	r.onUnregister = fn
	r.mu.Unlock()
}

// Register associates a connection ID with its send function. The send
// function writes a Payload frame on the BiRequestStream. Calling Register
// with an existing ID replaces the previous sender.
func (r *ConnectionRegistry) Register(connID string, send func(Payload) error) {
	if connID == "" || send == nil {
		return
	}
	r.mu.Lock()
	r.senders[connID] = &connectionSender{send: send}
	r.mu.Unlock()
}

// Unregister removes a connection. Safe to call multiple times.
func (r *ConnectionRegistry) Unregister(connID string) {
	if connID == "" {
		return
	}
	r.mu.Lock()
	sender := r.senders[connID]
	delete(r.senders, connID)
	onUnregister := r.onUnregister
	r.mu.Unlock()
	if sender != nil {
		sender.mu.Lock()
		sender.closed = true
		sender.mu.Unlock()
		if onUnregister != nil {
			onUnregister(connID)
		}
	}
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
	send.mu.Lock()
	defer send.mu.Unlock()
	if send.closed {
		return false
	}
	return send.send(payload) == nil
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
