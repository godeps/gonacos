package app

import (
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
// created with a nil LLM client (stubbed).
func NewServiceBundle() *ServiceBundle {
	configSvc := configsvc.NewService()
	namingSvc := namingsvc.NewService()
	authSvc := authsvc.NewService()
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
	if services == nil {
		services = NewServiceBundle()
	}
	if coord == nil {
		coord = store.NewCoordinator()
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
	register("GET", "/v3/console/health/readiness", okHandler("ok"))
	register("GET", "/v3/admin/core/state/liveness", okHandler("ok"))
	register("GET", "/v3/admin/core/state/readiness", okHandler("ok"))
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

	registry := observability.NewRegistry()

	registerNamespaceRoutes(register, namespaceSvc)
	registerConfigRoutes(register, configSvc)
	registerNamingRoutes(register, namingSvc)
	registerAuthRoutes(register, authSvc)
	registerAIRoutes(register, aiSvc)
	registerClusterRoutes(register, clusterSvc)
	registerOpsRoutes(register, coord, registry)
	registerStubRoutes(register, configSvc, namingSvc, aiSvc, clusterSvc)

	mux.HandleFunc("/v3/console/ui", web.ConsoleHandler())
	mux.HandleFunc("/v3/console/ui/", web.ConsoleHandler())

	manifest, err := contract.Build(root)
	if err == nil {
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
	})
}

func requestPort(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err == nil && port != "" {
		return port
	}
	return "8848"
}
