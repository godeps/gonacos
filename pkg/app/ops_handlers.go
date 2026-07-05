package app

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"time"

	"github.com/godeps/gonacos/pkg/observability"
	"github.com/godeps/gonacos/pkg/protocol"
	"github.com/godeps/gonacos/pkg/store"
)

// opsHandler wires observability and backup endpoints. The snapshot
// coordinator is shared with the long-running service instances so a backup
// captures live state.
type opsHandler struct {
	coordinator *store.Coordinator
	registry    *observability.Registry
	refresh     func()
	audit       AuditLogger
}

func registerOpsRoutes(
	register func(string, string, http.HandlerFunc),
	coordinator *store.Coordinator,
	registry *observability.Registry,
	audit AuditLogger,
) {
	h := opsHandler{coordinator: coordinator, registry: registry, refresh: nil, audit: audit}
	if registry != nil {
		h.refresh = registry.RegisterProcessMetrics()
	}

	register(http.MethodGet, "/v3/admin/ops/metrics", h.metrics)
	register(http.MethodGet, "/v3/admin/ops/info", h.info)
	register(http.MethodGet, "/v3/admin/ops/backup", h.backup)
	register(http.MethodPost, "/v3/admin/ops/restore", h.restore)

	register(http.MethodGet, "/v3/admin/ops/pprof/", pprof.Index)
	register(http.MethodGet, "/v3/admin/ops/pprof/cmdline", pprof.Cmdline)
	register(http.MethodGet, "/v3/admin/ops/pprof/profile", pprof.Profile)
	register(http.MethodGet, "/v3/admin/ops/pprof/symbol", pprof.Symbol)
	register(http.MethodGet, "/v3/admin/ops/pprof/trace", pprof.Trace)
	register(http.MethodGet, "/v3/admin/ops/pprof/allocs", pprof.Handler("allocs").ServeHTTP)
	register(http.MethodGet, "/v3/admin/ops/pprof/heap", pprof.Handler("heap").ServeHTTP)
	register(http.MethodGet, "/v3/admin/ops/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
	register(http.MethodGet, "/v3/admin/ops/pprof/block", pprof.Handler("block").ServeHTTP)
	register(http.MethodGet, "/v3/admin/ops/pprof/mutex", pprof.Handler("mutex").ServeHTTP)
	register(http.MethodGet, "/v3/admin/ops/pprof/threadcreate", pprof.Handler("threadcreate").ServeHTTP)
}

// RegisterPublicMetrics registers /metrics on the given mux as the standard
// Prometheus scrape path. Unlike /v3/admin/ops/metrics (which goes through
// `register` and gets a /nacos-prefixed twin), /metrics is registered raw so
// Prometheus can scrape it with the default job config.
//
// When metricsToken is non-empty, the endpoint requires a Bearer token
// matching it (Authorization: Bearer <token>). A request without a valid
// token receives 401. When metricsToken is empty, the endpoint is publicly
// accessible — appropriate for development or when the network layer
// already restricts access (e.g., firewall rules, mTLS). Production
// deployments should set a token to avoid leaking process and business
// metrics to unauthenticated callers.
func RegisterPublicMetrics(mux *http.ServeMux, registry *observability.Registry, metricsToken string) {
	if registry == nil {
		return
	}
	h := opsHandler{registry: registry, refresh: registry.RegisterProcessMetrics()}
	var handler http.Handler = http.HandlerFunc(h.metrics)
	if metricsToken != "" {
		handler = newMetricsTokenMiddleware(metricsToken, handler)
	}
	mux.Handle("/metrics", handler)
}

// newMetricsTokenMiddleware guards the wrapped handler with a Bearer token
// check. The token comparison uses constant time to prevent timing attacks.
// A missing or malformed Authorization header returns 401 with a
// WWW-Authenticate challenge so scrapers configured with a token retry
// cleanly.
func newMetricsTokenMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			w.Header().Set("WWW-Authenticate", "Bearer realm=\"gonacos metrics\"")
			http.Error(w, "metrics endpoint requires a Bearer token", http.StatusUnauthorized)
			return
		}
		provided := strings.TrimPrefix(auth, prefix)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer realm=\"gonacos metrics\"")
			http.Error(w, "invalid metrics token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h opsHandler) metrics(w http.ResponseWriter, r *http.Request) {
	if h.registry == nil {
		protocol.WriteError(w, http.StatusNotImplemented, protocol.Error{
			Code:    protocol.CodeNotImplemented,
			Message: "metrics registry not configured",
		})
		return
	}
	if h.refresh != nil {
		h.refresh()
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	h.registry.WritePrometheus(w)
}

func (h opsHandler) info(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"version":          Version,
		"goroutines":       runtime.NumGoroutine(),
		"heapAllocBytes":   mem.HeapAlloc,
		"heapObjects":      mem.HeapObjects,
		"sysBytes":         mem.Sys,
		"gcCount":          mem.NumGC,
		"now":              time.Now().UTC().Format(time.RFC3339),
		"startTimeSeconds": time.Now().Unix(),
	})
}

func (h opsHandler) backup(w http.ResponseWriter, r *http.Request) {
	if h.coordinator == nil {
		protocol.WriteError(w, http.StatusNotImplemented, protocol.Error{
			Code:    protocol.CodeNotImplemented,
			Message: "snapshot coordinator not configured",
		})
		return
	}
	env, err := h.coordinator.Snapshot()
	if err != nil {
		auditLog(h.audit, r, AuditActionBackup, "", err.Error(), AuditResultFailure)
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    500,
			Message: err.Error(),
		})
		return
	}
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		auditLog(h.audit, r, AuditActionBackup, "", err.Error(), AuditResultFailure)
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    500,
			Message: err.Error(),
		})
		return
	}
	auditLog(h.audit, r, AuditActionBackup, "", fmt.Sprintf("services=%d", len(env.Services)), AuditResultSuccess)
	filename := "gonacos-backup-" + time.Now().UTC().Format("20060102-150405") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h opsHandler) restore(w http.ResponseWriter, r *http.Request) {
	if h.coordinator == nil {
		protocol.WriteError(w, http.StatusNotImplemented, protocol.Error{
			Code:    protocol.CodeNotImplemented,
			Message: "snapshot coordinator not configured",
		})
		return
	}
	var env store.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		auditLog(h.audit, r, AuditActionRestore, "", "invalid payload: "+err.Error(), AuditResultFailure)
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterValidateError,
			Message: "invalid backup payload: " + err.Error(),
		})
		return
	}
	if err := h.coordinator.Restore(&env); err != nil {
		auditLog(h.audit, r, AuditActionRestore, "", err.Error(), AuditResultFailure)
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    500,
			Message: err.Error(),
		})
		return
	}
	auditLog(h.audit, r, AuditActionRestore, "", fmt.Sprintf("services=%d", len(env.Services)), AuditResultSuccess)
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"version":    env.Version,
		"services":   serviceNames(env.Services),
		"restoredAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func serviceNames(services map[string]any) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	return names
}
