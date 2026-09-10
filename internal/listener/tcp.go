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
	"github.com/milosursulovic/vortex/internal/netutil"
	"github.com/milosursulovic/vortex/internal/proxy"
)

// Manager owns a set of TCP listeners and the goroutines serving them.
type Manager struct {
	logger      *slog.Logger
	connLimiter *limits.ConnLimiter
	rec         *metrics.Recorder
	netCfg      config.NetworkConfig
	listeners   []net.Listener
	wg          sync.WaitGroup
}

// NewManager creates an empty listener manager. connLimiter may be nil (or
// configured with no max) to leave the global connection count unbounded.
func NewManager(logger *slog.Logger, connLimiter *limits.ConnLimiter, rec *metrics.Recorder, netCfg config.NetworkConfig) *Manager {
	return &Manager{logger: logger, connLimiter: connLimiter, rec: rec, netCfg: netCfg}
}

// netutilConfig translates the configured network tuning into netutil's
// terms, only setting overrides that were actually configured.
func (m *Manager) netutilConfig() netutil.Config {
	cfg := netutil.Config{
		KeepAliveEnabled: m.netCfg.KeepAliveEnabled == nil || *m.netCfg.KeepAliveEnabled,
		KeepAlive:        m.netCfg.KeepAlive.Duration(),
		ReadBufferBytes:  m.netCfg.ReadBufferBytes,
		WriteBufferBytes: m.netCfg.WriteBufferBytes,
	}
	if m.netCfg.TCPNoDelay != nil {
		cfg.NoDelaySet = true
		cfg.TCPNoDelay = *m.netCfg.TCPNoDelay
	}
	return cfg
}

// reusePortWorkers returns how many parallel listening sockets cfg should
// use: its own override if set, otherwise the global default. <=1 means
// "just one listener" (SO_REUSEPORT not needed).
func (m *Manager) reusePortWorkers(cfg config.ListenerConfig) int {
	if cfg.ReusePortWorkers > 0 {
		return cfg.ReusePortWorkers
	}
	return m.netCfg.ReusePortWorkers
}

// bind opens either a single listener or, if reuse_port_workers > 1 for
// this listener, a SO_REUSEPORT group of them all bound to the same
// address — so multiple accept loops can share the accept load across
// cores instead of one goroutine serializing every accept.
func (m *Manager) bind(cfg config.ListenerConfig) ([]net.Listener, error) {
	netCfg := m.netutilConfig()
	workers := m.reusePortWorkers(cfg)

	if workers <= 1 {
		ln, err := netutil.Listen(context.Background(), cfg.Address, netCfg)
		if err != nil {
			return nil, fmt.Errorf("listen %s: %w", cfg.Address, err)
		}
		return []net.Listener{ln}, nil
	}

	lns, err := netutil.NewReusePortGroup(context.Background(), cfg.Address, workers, netCfg)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", cfg.Address, err)
	}
	m.logger.Info("reuse_port_enabled", "listener", cfg.Name, "address", cfg.Address, "workers", workers)
	return lns, nil
}

// StartTCP binds cfg.Address (as one listener, or a SO_REUSEPORT group —
// see bind) and begins accepting connections on each, proxying every
// connection to a backend chosen by picker. It returns once bound;
// accepting happens in background goroutines.
func (m *Manager) StartTCP(cfg config.ListenerConfig, picker proxy.BackendPicker, timeouts config.TimeoutsConfig, limitsCfg config.LimitsConfig) error {
	lns, err := m.bind(cfg)
	if err != nil {
		return err
	}

	for _, ln := range lns {
		ln = limits.WrapListener(ln, m.connLimiter, m.logger, cfg.Name)
		m.listeners = append(m.listeners, ln)
		m.logger.Info("listener_started", "component", "tcp", "name", cfg.Name, "address", cfg.Address)

		m.wg.Add(1)
		go func(ln net.Listener) {
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
		}(ln)
	}

	return nil
}

// StartHTTP binds cfg.Address (as one listener, or a SO_REUSEPORT group —
// see bind) and serves handler over HTTP/1.1 on each, applying the
// configured read/write/idle timeouts. If tlsConfig is non-nil, every
// listener terminates TLS before requests reach handler. It returns once
// bound; serving happens in background goroutines.
func (m *Manager) StartHTTP(cfg config.ListenerConfig, handler http.Handler, timeouts config.TimeoutsConfig, tlsConfig *tls.Config, limitsCfg config.LimitsConfig) error {
	lns, err := m.bind(cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  timeouts.Read.Duration(),
		WriteTimeout: timeouts.Write.Duration(),
		IdleTimeout:  timeouts.Idle.Duration(),
	}
	if limitsCfg.MaxHeaderSizeKB > 0 {
		srv.MaxHeaderBytes = limitsCfg.MaxHeaderSizeKB * 1024
	}

	proto := "http"
	if tlsConfig != nil {
		proto = "https"
	}

	for _, ln := range lns {
		ln = limits.WrapListener(ln, m.connLimiter, m.logger, cfg.Name)
		if tlsConfig != nil {
			ln = tls.NewListener(ln, tlsConfig)
		}
		m.listeners = append(m.listeners, ln)
		m.logger.Info("listener_started", "component", proto, "name", cfg.Name, "address", cfg.Address)

		m.wg.Add(1)
		go func(ln net.Listener) {
			defer m.wg.Done()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				m.logger.Error("http_serve_error", "listener", cfg.Name, "error", err.Error())
			}
		}(ln)
	}

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
