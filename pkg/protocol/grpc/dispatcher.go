package grpc

import (
	"context"
	"strings"
	"sync"
)

// UnaryDispatcher routes unary RPCs by Metadata.type. The Nacos SDK sends a
// Payload whose Metadata.type names the concrete request type (e.g.
// "InstanceRequest", "ConfigPublishRequest"). Each registered handler claims
// one or more type names.
type UnaryDispatcher struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewUnaryDispatcher returns an empty dispatcher.
func NewUnaryDispatcher() *UnaryDispatcher {
	return &UnaryDispatcher{handlers: map[string]Handler{}}
}

// Register maps a request type to a handler.
func (d *UnaryDispatcher) Register(typeName string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[strings.TrimSpace(typeName)] = h
}

// Handle routes by Metadata.type. Unknown types return Unimplemented.
func (d *UnaryDispatcher) Handle(ctx context.Context, req Payload) (Payload, error) {
	typeName := strings.TrimSpace(req.Metadata.Type)
	if typeName == "" {
		return Payload{}, NewStatusError(StatusInvalidArgument, "metadata.type is required")
	}
	d.mu.RLock()
	h, ok := d.handlers[typeName]
	d.mu.RUnlock()
	if !ok {
		return Payload{}, NewStatusError(StatusUnimplemented, "unsupported request type: "+typeName)
	}
	return h(ctx, req)
}

// StreamDispatcher routes server-streaming RPCs by Metadata.type.
type StreamDispatcher struct {
	mu       sync.RWMutex
	handlers map[string]StreamHandler
}

// NewStreamDispatcher returns an empty dispatcher.
func NewStreamDispatcher() *StreamDispatcher {
	return &StreamDispatcher{handlers: map[string]StreamHandler{}}
}

// Register maps a request type to a stream handler.
func (d *StreamDispatcher) Register(typeName string, h StreamHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[strings.TrimSpace(typeName)] = h
}

// Handle routes by Metadata.type.
func (d *StreamDispatcher) Handle(ctx context.Context, req Payload, send func(Payload) error) error {
	typeName := strings.TrimSpace(req.Metadata.Type)
	if typeName == "" {
		return NewStatusError(StatusInvalidArgument, "metadata.type is required")
	}
	d.mu.RLock()
	h, ok := d.handlers[typeName]
	d.mu.RUnlock()
	if !ok {
		return NewStatusError(StatusUnimplemented, "unsupported stream type: "+typeName)
	}
	return h(ctx, req, send)
}

// BiStreamDispatcher routes bidi-streaming RPCs by Metadata.type. The Nacos
// SDK uses the BiRequestStream service for connection setup and
// subscribe/unsubscribe notifications; each frame's Metadata.type selects the
// handler.
type BiStreamDispatcher struct {
	mu       sync.RWMutex
	handlers map[string]BiStreamHandler
}

// NewBiStreamDispatcher returns an empty dispatcher.
func NewBiStreamDispatcher() *BiStreamDispatcher {
	return &BiStreamDispatcher{handlers: map[string]BiStreamHandler{}}
}

// Register maps a request type to a bi-stream handler.
func (d *BiStreamDispatcher) Register(typeName string, h BiStreamHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[strings.TrimSpace(typeName)] = h
}

// Handle routes by Metadata.type.
func (d *BiStreamDispatcher) Handle(ctx context.Context, recv func() (Payload, error), send func(Payload) error) error {
	// Read the first frame to discover the type, then dispatch.
	first, err := recv()
	if err != nil {
		return err
	}
	typeName := strings.TrimSpace(first.Metadata.Type)
	if typeName == "" {
		return NewStatusError(StatusInvalidArgument, "metadata.type is required")
	}
	d.mu.RLock()
	h, ok := d.handlers[typeName]
	d.mu.RUnlock()
	if !ok {
		return NewStatusError(StatusUnimplemented, "unsupported bi-stream type: "+typeName)
	}
	// Re-inject the first frame so the handler sees the full stream.
	replay := append([]Payload{first}, nil...)
	replayChan := make(chan Payload, 1)
	replayChan <- first
	combinedRecv := func() (Payload, error) {
		select {
		case p := <-replayChan:
			return p, nil
		default:
			return recv()
		}
	}
	_ = replay
	return h(ctx, combinedRecv, send)
}
