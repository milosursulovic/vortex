// Package admin exposes VORTEX's internal HTTP server: health checks,
// backend management, runtime statistics, config inspection, and reload —
// never intended to be exposed publicly.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/metrics"
)

// ReloadFunc re-reads the config file and applies whatever of it can be
// safely changed live. It returns an error if the reload could not be
// completed (e.g. the file is invalid).
type ReloadFunc func() error

// Server is VORTEX's internal admin/health HTTP server.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	registry   *Registry
	stats      *metrics.Stats
	cfg        atomic.Pointer[config.Config]
	reload     ReloadFunc
}

// New builds an admin server bound to address, not yet listening.
func New(address string, logger *slog.Logger, registry *Registry, stats *metrics.Stats, cfg *config.Config, reload ReloadFunc) *Server {
	s := &Server{logger: logger, registry: registry, stats: stats, reload: reload}
	s.cfg.Store(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /admin/backends", s.handleListBackends)
	mux.HandleFunc("GET /admin/backends/{name}", s.handleGetBackend)
	mux.HandleFunc("POST /admin/backends/{name}/enable", s.handleBackendAction(s.registry.Enable, "enable", "enabled"))
	mux.HandleFunc("POST /admin/backends/{name}/disable", s.handleBackendAction(s.registry.Disable, "disable", "disabled"))
	mux.HandleFunc("POST /admin/backends/{name}/drain", s.handleBackendAction(s.registry.Drain, "drain", "drained"))
	mux.HandleFunc("GET /admin/stats", s.handleStats)
	mux.HandleFunc("GET /admin/config", s.handleConfig)
	mux.HandleFunc("POST /admin/reload", s.handleReload)

	s.httpServer = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	return s
}

// UpdateConfig replaces what GET /admin/config reports, e.g. after a
// successful reload.
func (s *Server) UpdateConfig(cfg *config.Config) {
	s.cfg.Store(cfg)
}

// Start begins serving in a goroutine and returns immediately. Bind errors
// other than a clean shutdown are sent on the returned channel.
func (s *Server) Start() <-chan error {
	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("listener_started", "component", "admin", "address", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	return errCh
}

// Shutdown gracefully stops the server, waiting up to ctx's deadline for
// in-flight requests to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListBackends(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.registry.List())
}

func (s *Server) handleGetBackend(w http.ResponseWriter, r *http.Request) {
	info, ok := s.registry.Find(r.PathValue("name"))
	if !ok {
		http.Error(w, "backend not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleBackendAction(action func(string) bool, verb, pastTense string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !action(name) {
			http.Error(w, "backend not found", http.StatusNotFound)
			return
		}
		s.logger.Info("backend_"+pastTense+"_via_admin", "backend", name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "backend": name, "action": verb})
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stats.Snapshot())
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Load())
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.reload == nil {
		http.Error(w, "reload not available", http.StatusNotImplemented)
		return
	}
	if err := s.reload(); err != nil {
		s.logger.Error("reload_failed", "error", err.Error())
		http.Error(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
