package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// RegisterInternalHandlers wires the connection setup, server check, and
// health check handlers. The SDK sends these before any config/naming
// requests, so they must be registered for the client to connect.
func RegisterInternalHandlers(d *UnaryDispatcher) {
	if d == nil {
		return
	}
	d.Register("ServerCheckRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return buildResponse("ServerCheckResponse", map[string]any{
			"resultCode":   200,
			"success":      true,
			"message":      "ok",
			"connectionId": generateConnectionID(),
		}), nil
	})
	d.Register("ConnectionSetupRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return buildResponse("ConnectionSetupResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		}), nil
	})
	d.Register("HealthCheckRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return buildResponse("HealthCheckResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		}), nil
	})
	d.Register("ClientDetectionRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return buildResponse("ClientDetectionResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		}), nil
	})
	d.Register("ConnectResetRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return buildResponse("ConnectResetResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		}), nil
	})
}

// generateConnectionID returns a unique connection ID for each server check.
var connCounter int64

func generateConnectionID() string {
	n := atomic.AddInt64(&connCounter, 1)
	return fmt.Sprintf("gonacos-%d-%d", time.Now().UnixNano(), n)
}

// RegisterNamingHandlers wires the naming service into the unary dispatcher.
// The Nacos SDK sends InstanceRequest to register/deregister instances via
// gRPC. The body is JSON-serialized in google.protobuf.Any.Value.
func RegisterNamingHandlers(d *UnaryDispatcher, naming NamingAdapter) {
	if naming == nil || d == nil {
		return
	}
	d.Register("InstanceRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return handleInstanceRequest(naming, req)
	})
	d.Register("SubscribeServiceRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return handleSubscribeRequest(naming, req, ClientIPFromContext(ctx))
	})
	d.Register("BatchInstanceRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return handleBatchInstanceRequest(naming, req)
	})
	d.Register("ServiceQueryRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return handleServiceQueryRequest(naming, req)
	})
	d.Register("ServiceListRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return handleServiceListRequest(naming, req)
	})
	d.Register("NotifySubscriberRequest", func(ctx context.Context, req Payload) (Payload, error) {
		return buildResponse("NotifySubscriberResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
		}), nil
	})
}

// NamingAdapter is the subset of the naming service the gRPC layer needs.
// Defined here to avoid an import cycle with internal/naming.
// The body parameter is the JSON-serialized request from Any.Value.
type NamingAdapter interface {
	RegisterInstanceFromGRPC(body []byte) (any, error)
	DeregisterInstanceFromGRPC(body []byte) error
	SubscribeFromGRPC(body []byte, clientIP string) (any, error)
	ListInstancesFromGRPC(body []byte) (any, error)
	QueryServiceFromGRPC(body []byte) (any, error)
	ListServicesFromGRPC(body []byte) (any, error)
}

func handleInstanceRequest(naming NamingAdapter, req Payload) (Payload, error) {
	type instanceRequest struct {
		Type      string `json:"type"`
		Namespace string `json:"namespace"`
		Group     string `json:"group"`
		Service   string `json:"serviceName"`
		Op        string `json:"requestType"`
	}
	var r instanceRequest
	if err := json.Unmarshal(req.Body.Value, &r); err != nil {
		return Payload{}, NewStatusError(StatusInvalidArgument, "invalid instance request: "+err.Error())
	}
	op := strings.ToLower(strings.TrimSpace(r.Op))
	if op == "" {
		op = "register"
	}
	switch op {
	case "register", "register_instance":
		result, err := naming.RegisterInstanceFromGRPC(req.Body.Value)
		if err != nil {
			return buildErrorResponse("InstanceResponse", err), nil
		}
		return buildResponse("InstanceResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
			"data":       result,
		}), nil
	case "deregister", "deregister_instance":
		if err := naming.DeregisterInstanceFromGRPC(req.Body.Value); err != nil {
			return buildErrorResponse("InstanceResponse", err), nil
		}
		return buildResponse("InstanceResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		}), nil
	default:
		return buildErrorResponse("InstanceResponse", fmt.Errorf("unknown instance op: %s", op)), nil
	}
}

func handleSubscribeRequest(naming NamingAdapter, req Payload, clientIP string) (Payload, error) {
	result, err := naming.SubscribeFromGRPC(req.Body.Value, clientIP)
	if err != nil {
		return buildErrorResponse("SubscribeServiceResponse", err), nil
	}
	return buildResponse("SubscribeServiceResponse", map[string]any{
		"resultCode":   200,
		"success":      true,
		"message":      "ok",
		"serviceInfo":  result,
	}), nil
}

func handleBatchInstanceRequest(naming NamingAdapter, req Payload) (Payload, error) {
	result, err := naming.RegisterInstanceFromGRPC(req.Body.Value)
	if err != nil {
		return buildErrorResponse("BatchInstanceResponse", err), nil
	}
	return buildResponse("BatchInstanceResponse", map[string]any{
		"resultCode": 200,
		"success":    true,
		"message":    "ok",
		"data":       result,
	}), nil
}

func handleServiceQueryRequest(naming NamingAdapter, req Payload) (Payload, error) {
	result, err := naming.QueryServiceFromGRPC(req.Body.Value)
	if err != nil {
		return buildErrorResponse("QueryServiceResponse", err), nil
	}
	return buildResponse("QueryServiceResponse", map[string]any{
		"resultCode":   200,
		"success":      true,
		"message":      "ok",
		"serviceInfo":  result,
	}), nil
}

func handleServiceListRequest(naming NamingAdapter, req Payload) (Payload, error) {
	result, err := naming.ListServicesFromGRPC(req.Body.Value)
	if err != nil {
		return buildErrorResponse("ServiceListResponse", err), nil
	}
	return buildResponse("ServiceListResponse", map[string]any{
		"resultCode": 200,
		"success":    true,
		"message":    "ok",
		"count":      result.(map[string]any)["count"],
		"serviceInfos": result.(map[string]any)["services"],
	}), nil
}

// RegisterConfigHandlers wires the config service into the unary dispatcher.
func RegisterConfigHandlers(d *UnaryDispatcher, config ConfigAdapter) {
	if config == nil || d == nil {
		return
	}
	d.Register("ConfigPublishRequest", func(ctx context.Context, req Payload) (Payload, error) {
		result, err := config.PublishFromGRPC(req.Body.Value)
		if err != nil {
			return buildErrorResponse("ConfigPublishResponse", err), nil
		}
		return buildResponse("ConfigPublishResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
			"data":       result,
		}), nil
	})
	d.Register("ConfigQueryRequest", func(ctx context.Context, req Payload) (Payload, error) {
		result, err := config.QueryFromGRPC(req.Body.Value)
		if err != nil {
			return buildErrorResponse("ConfigQueryResponse", err), nil
		}
		return buildResponse("ConfigQueryResponse", result), nil
	})
	d.Register("ConfigRemoveRequest", func(ctx context.Context, req Payload) (Payload, error) {
		_, err := config.RemoveFromGRPC(req.Body.Value)
		if err != nil {
			return buildErrorResponse("ConfigRemoveResponse", err), nil
		}
		return buildResponse("ConfigRemoveResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		}), nil
	})
	d.Register("ConfigBatchPublishRequest", func(ctx context.Context, req Payload) (Payload, error) {
		result, err := config.BatchPublishFromGRPC(req.Body.Value)
		if err != nil {
			return buildErrorResponse("ConfigBatchPublishResponse", err), nil
		}
		return buildResponse("ConfigBatchPublishResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
			"data":       result,
		}), nil
	})
	d.Register("ConfigBatchListenRequest", func(ctx context.Context, req Payload) (Payload, error) {
		ip := ClientIPFromContext(ctx)
		result, err := config.BatchListenFromGRPC(req.Body.Value, ip)
		if err != nil {
			return buildErrorResponse("ConfigChangeBatchListenResponse", err), nil
		}
		resp, _ := result.(map[string]any)
		if resp == nil {
			resp = map[string]any{}
		}
		resp["resultCode"] = 200
		resp["success"] = true
		resp["message"] = "ok"
		if _, ok := resp["changedConfigs"]; !ok {
			resp["changedConfigs"] = []any{}
		}
		return buildResponse("ConfigChangeBatchListenResponse", resp), nil
	})
}

// ConfigAdapter is the subset of the config service the gRPC layer needs.
type ConfigAdapter interface {
	PublishFromGRPC(body []byte) (any, error)
	QueryFromGRPC(body []byte) (any, error)
	RemoveFromGRPC(body []byte) (any, error)
	BatchPublishFromGRPC(body []byte) (any, error)
	BatchListenFromGRPC(body []byte, ip string) (any, error)
}

// RegisterAIHandlers wires the AI service into the unary dispatcher for
// prompt/skill queries the SDK may issue.
func RegisterAIHandlers(d *UnaryDispatcher, ai AIAdapter) {
	if ai == nil || d == nil {
		return
	}
	d.Register("PromptRequest", func(ctx context.Context, req Payload) (Payload, error) {
		result, err := ai.QueryPromptFromGRPC(req.Body.Value)
		if err != nil {
			return Payload{}, NewStatusError(StatusInternal, err.Error())
		}
		return buildResponse("PromptResponse", result), nil
	})
	d.Register("SkillRequest", func(ctx context.Context, req Payload) (Payload, error) {
		result, err := ai.QuerySkillFromGRPC(req.Body.Value)
		if err != nil {
			return Payload{}, NewStatusError(StatusInternal, err.Error())
		}
		return buildResponse("SkillResponse", result), nil
	})
}

// AIAdapter is the subset of the AI service the gRPC layer needs.
type AIAdapter interface {
	QueryPromptFromGRPC(body []byte) (any, error)
	QuerySkillFromGRPC(body []byte) (any, error)
}

// extractStringField reads a string field from a protobuf message by field
// number. Returns "" if not found. Retained for AI adapter use.
func extractStringField(body []byte, field int) string {
	r := NewReader(body)
	for !r.Done() {
		f, wire, err := r.ReadTag()
		if err != nil {
			return ""
		}
		if f == field && wire == wireBytes {
			s, err := r.ReadString()
			if err != nil {
				return ""
			}
			return s
		}
		if err := r.Skip(wire); err != nil {
			return ""
		}
	}
	return ""
}

var _ = extractStringField // retain for future use

// buildResponse constructs a Payload with the given type and JSON-encoded data.
// The body is a google.protobuf.Any with a JSON type_url and the raw JSON bytes.
func buildResponse(typeName string, data any) Payload {
	return Payload{
		Metadata: Metadata{
			Type:    typeName,
			Headers: map[string]string{},
		},
		Body: Any{
			TypeURL: "type.googleapis.com/" + typeName,
			Value:   encodeResponseData(data),
		},
	}
}

// buildErrorResponse constructs an error response Payload with resultCode 500.
func buildErrorResponse(typeName string, err error) Payload {
	return Payload{
		Metadata: Metadata{
			Type:    typeName,
			Headers: map[string]string{},
		},
		Body: Any{
			TypeURL: "type.googleapis.com/" + typeName,
			Value:   encodeResponseData(map[string]any{
				"resultCode": 500,
				"errorCode":  500,
				"success":    false,
				"message":    err.Error(),
			}),
		},
	}
}

func encodeResponseData(data any) []byte {
	b, err := json.Marshal(data)
	if err != nil {
		return []byte(`{"resultCode":500,"success":false,"message":"encode error"}`)
	}
	return b
}

// DefaultDispatcher builds the standard unary dispatcher with all registered
// handlers. Pass nil for any adapter to skip that service.
func DefaultDispatcher(naming NamingAdapter, config ConfigAdapter, ai AIAdapter) *UnaryDispatcher {
	d := NewUnaryDispatcher()
	RegisterInternalHandlers(d)
	RegisterNamingHandlers(d, naming)
	RegisterConfigHandlers(d, config)
	RegisterAIHandlers(d, ai)
	return d
}

// SetupDefaultServer configures the package-level DefaultServer with the
// standard dispatcher and returns it. If a ConnectionRegistry is provided,
// the BiRequestStream handler registers each connection so the server can
// push notifications to subscribed clients.
func SetupDefaultServer(naming NamingAdapter, config ConfigAdapter, ai AIAdapter) *Server {
	return SetupDefaultServerWithRegistry(naming, config, ai, nil)
}

// SetupDefaultServerWithRegistry is like SetupDefaultServer but wires a
// ConnectionRegistry into the BiRequestStream handler for server push.
func SetupDefaultServerWithRegistry(naming NamingAdapter, config ConfigAdapter, ai AIAdapter, registry *ConnectionRegistry) *Server {
	srv := DefaultServer()
	d := DefaultDispatcher(naming, config, ai)
	srv.RegisterUnary("Request/request", d.Handle)
	// Register the stream service with a type-based dispatch.
	streamD := NewStreamDispatcher()
	srv.RegisterUnary("RequestStream/requestStream", func(ctx context.Context, req Payload) (Payload, error) {
		var first Payload
		err := streamD.Handle(ctx, req, func(p Payload) error {
			if first.Metadata.Type == "" {
				first = p
			}
			return nil
		})
		if err != nil {
			return Payload{}, err
		}
		return first, nil
	})
	// The bi-stream service handles connection setup, health checks, and
	// client detection. When a registry is set, each connection is registered
	// so the server can push ConfigChangeNotify and NotifySubscriber frames.
	srv.RegisterBiStream("BiRequestStream/requestBiStream", func(ctx context.Context, recv func() (Payload, error), send func(Payload) error) error {
		return handleBiStreamConnectionWithRegistry(ctx, recv, send, registry)
	})
	return srv
}

// handleBiStreamConnectionWithRegistry is the BiRequestStream handler. It
// reads frames from the client and responds to connection setup, health
// check, and client detection requests. Other request types are acked with
// a success response. The loop exits when the client disconnects or sends
// a ConnectResetRequest.
//
// When a ConnectionRegistry is set, the connection is registered on
// ConnectionSetupRequest so the server can push notifications to the
// client. The connection is unregistered when the loop exits.
func handleBiStreamConnectionWithRegistry(ctx context.Context, recv func() (Payload, error), send func(Payload) error, registry *ConnectionRegistry) error {
	clientIP := ClientIPFromContext(ctx)
	registered := false
	defer func() {
		if registry != nil && registered && clientIP != "" {
			registry.Unregister(clientIP)
		}
	}()
	for {
		req, err := recv()
		if err != nil {
			return nil // client disconnected
		}
		typeName := strings.TrimSpace(req.Metadata.Type)
		if typeName == "ConnectionSetupRequest" && registry != nil && clientIP != "" {
			registry.Register(clientIP, send)
			registered = true
		}
		resp := buildConnectionResponse(typeName, req)
		if err := send(resp); err != nil {
			return err
		}
		if typeName == "ConnectResetRequest" {
			return nil
		}
	}
}

func buildConnectionResponse(typeName string, req Payload) Payload {
	switch typeName {
	case "ConnectionSetupRequest":
		return buildResponse("ConnectionSetupResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
			"connectionId": req.Metadata.Headers["connectionId"],
		})
	case "HealthCheckRequest":
		return buildResponse("HealthCheckResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		})
	case "ClientDetectionRequest":
		return buildResponse("ClientDetectionResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		})
	case "ConnectResetRequest":
		return buildResponse("ConnectResetResponse", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		})
	default:
		// Ack unknown types so the SDK doesn't hang.
		return buildResponse(typeName+"Response", map[string]any{
			"resultCode": 200,
			"success":    true,
			"message":    "ok",
		})
	}
}

// _ is a sentinel to keep time imported for future heartbeat timestamps.
var _ = time.Now
