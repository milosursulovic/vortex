// Package listener manages VORTEX's network listeners and hands accepted
// connections off to the proxy layer.
package listener

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/limits"
	"github.com/milosursulovic/vortex/internal/metrics"
	"github.com/milosursulovic/vortex/internal/proxy"
)

// Manager owns a set of TCP listeners and the goroutines serving them.
type Manager struct {
	logger      *slog.Logger
	connLimiter *limits.ConnLimiter
	rec         *metrics.Recorder
	listeners   []net.Listener
	wg          sync.WaitGroup
}

// NewManager creates an empty listener manager. connLimiter may be nil (or
// configured with no max) to leave the global connection count unbounded.
func NewManager(logger *slog.Logger, connLimiter *limits.ConnLimiter, rec *metrics.Recorder) *Manager {
	return &Manager{logger: logger, connLimiter: connLimiter, rec: rec}
}

// StartTCP binds cfg.Address and begins accepting connections, proxying
// each to a backend chosen by picker. It returns once the listener is bound;
// accepting happens in a background goroutine.
func (m *Manager) StartTCP(cfg config.ListenerConfig, picker proxy.BackendPicker, timeouts config.TimeoutsConfig, limitsCfg config.LimitsConfig) error {
	ln, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Address, err)
	}
	ln = limits.WrapListener(ln, m.connLimiter, m.logger, cfg.Name)
	m.listeners = append(m.listeners, ln)
	m.logger.Info("listener_started", "component", "tcp", "name", cfg.Name, "address", cfg.Address)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				m.logger.Error("accept_error", "listener", cfg.Name, "error", err.Error())
				continue
			}

			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				proxy.ServeTCP(conn, picker, timeouts, limitsCfg, m.rec, m.logger)
			}()
		}
	}()

	return nil
}

// StartHTTP binds cfg.Address and serves handler over HTTP/1.1, applying
// the configured read/write/idle timeouts. If tlsConfig is non-nil, the
// listener terminates TLS before requests reach handler. It returns once
// the listener is bound; serving happens in a background goroutine.
func (m *Manager) StartHTTP(cfg config.ListenerConfig, handler http.Handler, timeouts config.TimeoutsConfig, tlsConfig *tls.Config, limitsCfg config.LimitsConfig) error {
	ln, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Address, err)
	}
	ln = limits.WrapListener(ln, m.connLimiter, m.logger, cfg.Name)

	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
	}
	m.listeners = append(m.listeners, ln)

	proto := "http"
	if tlsConfig != nil {
		proto = "https"
	}
	m.logger.Info("listener_started", "component", proto, "name", cfg.Name, "address", cfg.Address)

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  timeouts.Read.Duration(),
		WriteTimeout: timeouts.Write.Duration(),
		IdleTimeout:  timeouts.Idle.Duration(),
	}
	if limitsCfg.MaxHeaderSizeKB > 0 {
		srv.MaxHeaderBytes = limitsCfg.MaxHeaderSizeKB * 1024
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			m.logger.Error("http_serve_error", "listener", cfg.Name, "error", err.Error())
		}
	}()

	return nil
}

// Close stops accepting new connections on all listeners. Connections
// already being served continue until they finish.
func (m *Manager) Close() error {
	var firstErr error
	for _, ln := range m.listeners {
		if err := ln.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WaitClosed blocks until every accept loop and in-flight connection has
// finished, or ctx is done, whichever comes first.
func (m *Manager) WaitClosed(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}
