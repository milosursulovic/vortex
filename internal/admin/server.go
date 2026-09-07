// Package admin exposes VORTEX's internal HTTP server: health checks now,
// the full admin API (backend management, stats, config) in a later phase.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Server is VORTEX's internal admin/health HTTP server.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// New builds an admin server bound to address, not yet listening.
func New(address string, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	s := &Server{logger: logger}
	mux.HandleFunc("GET /health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	return s
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
