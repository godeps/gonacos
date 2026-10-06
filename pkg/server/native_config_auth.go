package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

// NativeConfigAuthorizer validates an SDK credential against a config scope.
// Empty tenant, group, and dataID request credential validation only.
// The implementation must be safe for concurrent calls and check revocation.
type NativeConfigAuthorizer func(secret, tenant, group, dataID string) error

type nativeConfigGuard struct {
	authorize NativeConfigAuthorizer
	secrets   sync.Map // transport ID -> SDK secret; never emitted in logs
}

func (g *nativeConfigGuard) forget(connectionID string) {
	g.secrets.Delete(connectionID)
}

func (g *nativeConfigGuard) allowPush(connectionID, tenant, group, dataID string) bool {
	secret, ok := g.secrets.Load(connectionID)
	return ok && g.authorize(secret.(string), tenant, group, dataID) == nil
}

func nativeConfigGRPCAuthorizer(authorize NativeConfigAuthorizer) func(context.Context, string, grpcsrv.Payload) error {
	return (&nativeConfigGuard{authorize: authorize}).authorizeRequest
}

func (g *nativeConfigGuard) authorizeRequest(ctx context.Context, method string, req grpcsrv.Payload) error {
	switch req.Metadata.Type {
	case "ServerCheckRequest", "ConnectionSetupRequest", "HealthCheckRequest", "ClientDetectionRequest", "ConnectResetRequest":
		return nil
	case "ConfigQueryRequest", "ConfigBatchListenRequest":
		if method != "/Request/request" {
			return grpcsrv.NewStatusError(grpcsrv.StatusPermissionDenied, "request method denied")
		}
	default:
		return grpcsrv.NewStatusError(grpcsrv.StatusPermissionDenied, "request type denied")
	}
	secret := req.Metadata.Headers["accessToken"]
	if secret == "" {
		return grpcsrv.NewStatusError(grpcsrv.StatusUnauthenticated, "missing access token")
	}
	if err := g.authorize(secret, "", "", ""); err != nil {
		return grpcsrv.NewStatusError(grpcsrv.StatusUnauthenticated, "invalid access token")
	}
	if req.Metadata.Type == "ConfigQueryRequest" {
		var query struct {
			Tenant string `json:"tenant"`
			Group  string `json:"group"`
			DataID string `json:"dataId"`
		}
		if err := json.Unmarshal(req.Body.Value, &query); err != nil || query.Tenant == "" || query.Group == "" || query.DataID == "" {
			return grpcsrv.NewStatusError(grpcsrv.StatusInvalidArgument, "invalid config scope")
		}
		if err := g.authorize(secret, query.Tenant, query.Group, query.DataID); err != nil {
			return grpcsrv.NewStatusError(grpcsrv.StatusPermissionDenied, "config scope denied")
		}
		return nil
	}
	var listen struct {
		Contexts []struct {
			Tenant string `json:"tenant"`
			Group  string `json:"group"`
			DataID string `json:"dataId"`
		} `json:"configListenContexts"`
	}
	if err := json.Unmarshal(req.Body.Value, &listen); err != nil {
		return grpcsrv.NewStatusError(grpcsrv.StatusInvalidArgument, "invalid listen request")
	}
	for _, item := range listen.Contexts {
		if item.Tenant == "" || item.Group == "" || item.DataID == "" {
			return grpcsrv.NewStatusError(grpcsrv.StatusInvalidArgument, "invalid config scope")
		}
		if err := g.authorize(secret, item.Tenant, item.Group, item.DataID); err != nil {
			return grpcsrv.NewStatusError(grpcsrv.StatusPermissionDenied, "config scope denied")
		}
	}
	if connectionID := grpcsrv.ConnectionIDFromContext(ctx); connectionID != "" {
		actual, loaded := g.secrets.LoadOrStore(connectionID, secret)
		if loaded && actual != secret {
			return grpcsrv.NewStatusError(grpcsrv.StatusPermissionDenied, "multiple credentials on one transport")
		}
	}
	return nil
}

// nativeConfigHTTPHandler deliberately exposes only the official SDK's
// credential exchange. All normal gonacos HTTP APIs remain unavailable on
// this listener, so HTTP cannot bypass the gRPC scope checks.
func nativeConfigHTTPHandler(authorize NativeConfigAuthorizer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (r.URL.Path != "/nacos/v1/auth/users/login" && r.URL.Path != "/v1/auth/users/login") {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil || r.PostForm.Get("username") != "golab-sdk" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		secret := strings.TrimSpace(r.PostForm.Get("password"))
		if secret == "" || authorize(secret, "", "", "") != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": secret, "tokenTtl": 3600})
	})
}
