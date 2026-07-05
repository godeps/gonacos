package app

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	aivsvc "github.com/godeps/gonacos/pkg/ai"
	authsvc "github.com/godeps/gonacos/pkg/auth"
	clustersvc "github.com/godeps/gonacos/pkg/cluster"
	configsvc "github.com/godeps/gonacos/pkg/config"
	"github.com/godeps/gonacos/pkg/contract"
	"github.com/godeps/gonacos/pkg/namespace"
	namingsvc "github.com/godeps/gonacos/pkg/naming"
	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
	"github.com/godeps/gonacos/pkg/store"
	"github.com/godeps/gonacos/pkg/web"
)

const Version = "0.1.0-dev"

// ServiceBundle groups the shared service instances used by both the HTTP
// handler and the gRPC server so they see the same in-memory state. External
// embedders reach into the bundle via [Server.Services] to call service
// methods directly without a network hop.
type ServiceBundle struct {
	Namespace *namespace.Service
	Config    *configsvc.Service
	Naming    *namingsvc.Service
	Auth      *authsvc.Service
	AI        *aivsvc.Service
	Cluster   *clustersvc.Service
}

// NewServiceBundle builds a fresh set of service instances. Each service is
// constructed with its own zero-dependency NewService constructor; AI is
// created with a nil LLM client (stubbed). The auth service signs tokens with
// a random per-process secret — use [NewServiceBundleWithAuthSecret] when
// running multiple nodes that must verify each other's tokens.
func NewServiceBundle() *ServiceBundle {
	return NewServiceBundleWithAuthSecret("")
}

// NewServiceBundleWithAuthSecret builds a service bundle whose auth service
// signs tokens with the provided secret. An empty secret falls back to a
// random per-process secret (matching [NewServiceBundle]); use a non-empty
// shared secret when running multiple gonacos nodes behind a shared token
// domain so that a token issued by one node verifies on every other node.
func NewServiceBundleWithAuthSecret(authSecret string) *ServiceBundle {
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	var authSvc *authsvc.Service
	if authSecret != "" {
		authSvc = authsvc.NewServiceWithSecret(authSecret)
	} else {
		authSvc = authsvc.NewService()
	}
	aiSvc := aivsvc.NewService(nil)
	clusterSvc := clustersvc.NewService(clustersvc.ModeStandalone, "", 0, 0, 0)
	return &ServiceBundle{
		Namespace: namespace.NewService(),
		Config:    configSvc,
		Naming:    namingSvc,
		Auth:      authSvc,
		AI:        aiSvc,
		Cluster:   clusterSvc,
	}
}

// SetupGRPCServerWithPush builds the gRPC server with push support. When
// push is non-nil, the BiRequestStream handler registers connections so
// the server can push ConfigChangeNotify and NotifySubscriber frames to
// subscribed SDK clients.
func SetupGRPCServerWithPush(services *ServiceBundle, push *PushService) *grpcsrv.Server {
	var registry *grpcsrv.ConnectionRegistry
	if push != nil {
		registry = push.ConnectionRegistry()
	}
	return grpcsrv.SetupDefaultServerWithRegistry(
		namingGRPCAdapter{service: services.Naming, push: push},
		configGRPCAdapter{service: services.Config, push: push},
		aiGRPCAdapter{service: services.AI},
		registry,
	)
}

// NewHandler builds the HTTP handler with fresh service instances. Kept for
// tests and standalone runs that don't need a shared gRPC/HTTP bundle.
func NewHandler(root string) http.Handler {
	return NewHandlerWithServices(root, nil)
}

// NewHandlerWithServices builds the HTTP handler using the provided service
// bundle. If services is nil, fresh instances are created.
func NewHandlerWithServices(root string, services *ServiceBundle) http.Handler {
	return NewHandlerWithServicesWithCoordinator(root, services, nil)
}

// NewHandlerWithServicesWithCoordinator is like NewHandlerWithServices but
// accepts a shared *store.Coordinator. When coord is nil, a fresh coordinator
// is built and the services are registered into it. When coord is non-nil
// (passed from the server, which also uses it for startup restore and periodic
// save), the services are (re)registered into it so the HTTP backup/restore
// endpoints and the persistence layer share the same coordinator.
func NewHandlerWithServicesWithCoordinator(root string, services *ServiceBundle, coord *store.Coordinator) http.Handler {
	return NewHandlerWithServicesAndRegistry(root, services, coord, nil, nil, nil)
}

// NewHandlerWithServicesAndRegistry is like NewHandlerWithServicesWithCoordinator
// but also accepts a shared *observability.Registry, a ReadinessChecker, and
// a *LoginThrottle. When registry is nil, a fresh registry is created
// (matching the legacy behavior). When registry is non-nil (passed from the
// server, which also wires it into the push service), the HTTP /metrics
// endpoint and the push service share the same registry so scrapes see
// push-path counters alongside the HTTP handlers' counters. When readiness
// is nil, the /readiness endpoints always return 200/ok (matching the legacy
// behavior); pass a non-nil checker (e.g., a Redis Ping) to return 503 when
// a dependency is unreachable. When loginThrottle is nil, the /login
// endpoint is unprotected; pass a non-nil throttle to lock (ip, username)
// pairs after repeated failures.
func NewHandlerWithServicesAndRegistry(root string, services *ServiceBundle, coord *store.Coordinator, registry *observability.Registry, readiness ReadinessChecker, loginThrottle *LoginThrottle) http.Handler {
	if services == nil {
		services = NewServiceBundle()
	}
	if coord == nil {
		coord = store.NewCoordinator()
	}
	if registry == nil {
		registry = observability.NewRegistry()
	}

	mux := http.NewServeMux()
	routes := map[string]struct{}{}

	register := func(method, path string, handler http.HandlerFunc) {
		for _, routePath := range []string{path, "/nacos" + path} {
			pattern := method + " " + routePath
			if _, ok := routes[pattern]; ok {
				continue
			}
			routes[pattern] = struct{}{}
			mux.HandleFunc(pattern, handler)
		}
	}

	register("GET", "/v3/console/health/liveness", okHandler("ok"))
	register("GET", "/v3/console/health/readiness", readinessHandler(readiness))
	register("GET", "/v3/admin/core/state/liveness", okHandler("ok"))
	register("GET", "/v3/admin/core/state/readiness", readinessHandler(readiness))
	register("GET", "/v3/admin/core/state", stateHandler)
	register("GET", "/v3/console/server/state", stateHandler)
	register("GET", "/v3/console/server/announcement", okHandler(""))
	register("GET", "/v3/console/server/guide", okHandler(""))

	namespaceSvc := services.Namespace
	configSvc := services.Config
	namingSvc := services.Naming
	authSvc := services.Auth
	aiSvc := services.AI
	clusterSvc := services.Cluster

	coord.Register(namespaceSvc)
	coord.Register(configSvc)
	coord.Register(namingSvc)
	coord.Register(authSvc)
	coord.Register(aiSvc)
	coord.Register(clusterSvc)

	registerNamespaceRoutes(register, namespaceSvc, configSvc)
	registerConfigRoutes(register, configSvc)
	registerNamingRoutes(register, namingSvc)
	registerAuthRoutes(register, authSvc, loginThrottle)
	registerAIRoutes(register, aiSvc)
	registerClusterRoutes(register, clusterSvc)
	registerOpsRoutes(register, coord, registry)
	// Standard Prometheus scrape path (no /nacos prefix) so default
	// prometheus.yml `metrics_path: /metrics` works without configuration.
	RegisterPublicMetrics(mux, registry)
	registerStubRoutes(register, configSvc, namingSvc, aiSvc, clusterSvc)

	mux.Handle("GET /v3/console/ui", web.SpaHandler())
	mux.Handle("GET /v3/console/ui/", web.SpaHandler())
	// Legacy single-file console retained for backward compatibility.
	mux.Handle("GET /v3/console/ui/legacy", web.ConsoleHandler())
	mux.Handle("GET /v3/console/ui/legacy/", web.ConsoleHandler())

	manifest, err := contract.Build(root)
	if err != nil {
		log.Printf("app: contract build from root %q failed (%v); 501 stubs for unimplemented endpoints will not be registered", root, err)
	} else {
		for _, surface := range manifest.OpenAPI {
			for _, operation := range surface.Operations {
				op := operation
				register(op.Method, op.Path, func(w http.ResponseWriter, r *http.Request) {
					protocol.WriteError(w, http.StatusNotImplemented, protocol.Error{
						Code:    protocol.CodeNotImplemented,
						Message: "operation not implemented",
						Data: map[string]string{
							"method":      op.Method,
							"path":        op.Path,
							"operationId": op.OperationID,
						},
					})
				})
			}
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteError(w, http.StatusNotFound, protocol.Error{
			Code:    protocol.CodeNotFound,
			Message: "resource not found",
		})
	})

	return newAuthMiddleware(authSvc, mux)
}

func okHandler(data string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteResult(w, http.StatusOK, data)
	}
}

func stateHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UnixMilli()
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"standalone_mode":       "true",
		"function_mode":         "All",
		"version":               Version,
		"startup_mode":          "standalone",
		"server_port":           requestPort(r),
		"last_refresh_time":     strconv.FormatInt(now, 10),
		"last_refresh_time_str": time.UnixMilli(now).Format(time.RFC3339),
		// Fields expected by the Java React console (server-store.ts).
		"login_page_enabled":    "true",
		"auth_enabled":          "true",
		"console_ui_enabled":    "true",
		"auth_admin_request":    "true",
		"auth_system_type":      "nacos",
		"copilot_enabled":       "false",
		"ai_enabled":            "true",
		"config_retention_days": "30",
	})
}

func requestPort(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err == nil && port != "" {
		return port
	}
	return "8848"
}
