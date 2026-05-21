// Package healthz exposes a tiny HTTP listener for /healthz, /readyz, and
// /metrics.
//
// Distinct from a full HTTP API surface — the price-service speaks gRPC for
// application traffic. This listener exists so docker compose / k8s probes
// and Prometheus scrapers have something HTTP to talk to.
package healthz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/module"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// Module serves /healthz, /readyz, and /metrics on a configurable port.
//
// /healthz   — liveness, always 200 once the listener is up.
// /readyz    — readiness, 200 only when every registered module passes
//              HealthCheck. Surfaces aggregator warm-up + DB drops as 503.
// /metrics   — Prometheus exposition. Filled in by task 12; for now we
//              respond 200 with an empty body so the scrape configuration
//              can land ahead of the metric registration.
type Module struct {
	config   *config.HealthzConfig
	manager  *module.Manager // used to walk other modules for /readyz
	server   *http.Server
	listening atomic.Bool
}

// NewModule constructs the healthz module. manager is the application's
// global module manager, used to query peer modules for /readyz.
func NewModule(cfg *config.HealthzConfig, manager *module.Manager) *Module {
	return &Module{config: cfg, manager: manager}
}

// Name returns the module identifier.
func (m *Module) Name() string { return "healthz" }

// Init configures the http.Server but does not start it.
func (m *Module) Init(_ context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", m.handleHealthz)
	mux.HandleFunc("/readyz", m.handleReadyz)
	mux.HandleFunc("/metrics", m.handleMetrics)

	m.server = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", m.config.Host, m.config.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return nil
}

// Start begins serving HTTP. Non-blocking.
func (m *Module) Start(_ context.Context) error {
	go func() {
		m.listening.Store(true)
		defer m.listening.Store(false)
		logger.Log().Infof("healthz listening on %s", m.server.Addr)
		if err := m.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Log().Errorf("healthz server: %v", err)
		}
	}()
	return nil
}

// Stop shuts down the http.Server with a bounded deadline.
func (m *Module) Stop(ctx context.Context) error {
	if m.server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.server.Shutdown(shutdownCtx)
}

// HealthCheck reports the healthz module healthy once its listener is up.
func (m *Module) HealthCheck(_ context.Context) error {
	if !m.listening.Load() {
		return errors.New("healthz: listener not running")
	}
	return nil
}

// handleHealthz is a fixed-200 liveness probe.
func (m *Module) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"author": "andrei.solovov@gateway.fm",
	})
}

// handleReadyz polls every other module's HealthCheck via the manager.
func (m *Module) handleReadyz(w http.ResponseWriter, r *http.Request) {
	results := m.manager.HealthCheckAll(r.Context())
	failed := make(map[string]string)
	for name, err := range results {
		if name == m.Name() {
			// Self-check is meaningful only when we ARE serving the request;
			// skip to avoid a deadlock-style false negative.
			continue
		}
		if err != nil {
			failed[name] = err.Error()
		}
	}
	if len(failed) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"status":  "not_ready",
		"failing": failed,
	})
}

// handleMetrics is a stub. Wired to prometheus.Handler() in task 12.
func (m *Module) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	// TODO(task-12): replace with promhttp.Handler() once metrics registry lands.
	_, _ = w.Write([]byte("# metrics not yet wired\n"))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
