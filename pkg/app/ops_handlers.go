package app

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"runtime"
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
}

func registerOpsRoutes(
	register func(string, string, http.HandlerFunc),
	coordinator *store.Coordinator,
	registry *observability.Registry,
) {
	h := opsHandler{coordinator: coordinator, registry: registry, refresh: nil}
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
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    500,
			Message: err.Error(),
		})
		return
	}
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    500,
			Message: err.Error(),
		})
		return
	}
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
		protocol.WriteError(w, http.StatusBadRequest, protocol.Error{
			Code:    protocol.CodeParameterValidateError,
			Message: "invalid backup payload: " + err.Error(),
		})
		return
	}
	if err := h.coordinator.Restore(&env); err != nil {
		protocol.WriteError(w, http.StatusInternalServerError, protocol.Error{
			Code:    500,
			Message: err.Error(),
		})
		return
	}
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
