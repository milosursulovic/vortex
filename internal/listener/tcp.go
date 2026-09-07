// Package listener manages VORTEX's network listeners and hands accepted
// connections off to the proxy layer.
package listener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/proxy"
)

// Manager owns a set of TCP listeners and the goroutines serving them.
type Manager struct {
	logger    *slog.Logger
	listeners []net.Listener
	wg        sync.WaitGroup
}

// NewManager creates an empty listener manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// StartTCP binds cfg.Address and begins accepting connections, proxying
// each to a backend chosen by picker. It returns once the listener is bound;
// accepting happens in a background goroutine.
func (m *Manager) StartTCP(cfg config.ListenerConfig, picker proxy.BackendPicker, timeouts config.TimeoutsConfig) error {
	ln, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Address, err)
	}
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
				proxy.ServeTCP(conn, picker, timeouts, m.logger)
			}()
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
