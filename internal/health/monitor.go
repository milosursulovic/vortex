package health

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/config"
)

// Monitor periodically checks every backend in a pool and transitions it
// between UP and DOWN using consecutive-failure/success thresholds. A
// backend an operator has put into DRAINING is left alone: draining is an
// explicit override, not something health checks should undo.
type Monitor struct {
	pool    *backend.Pool
	checker Checker
	cfg     config.HealthCheckConfig
	logger  *slog.Logger

	mu     sync.Mutex
	states map[string]*counters
}

type counters struct {
	consecutiveFailures  int
	consecutiveSuccesses int
}

// NewMonitor builds a Monitor using the checker implied by cfg.Type.
func NewMonitor(pool *backend.Pool, cfg config.HealthCheckConfig, logger *slog.Logger) *Monitor {
	var checker Checker
	if cfg.Type == "http" {
		checker = NewHTTPChecker(cfg.Path)
	} else {
		checker = TCPChecker{}
	}

	return &Monitor{
		pool:    pool,
		checker: checker,
		cfg:     cfg,
		logger:  logger,
		states:  make(map[string]*counters),
	}
}

// Run checks all backends immediately, then again on every interval, until
// ctx is done. It returns immediately if health checking is disabled.
func (m *Monitor) Run(ctx context.Context) {
	if !m.cfg.Enabled {
		return
	}

	m.checkAll(ctx)

	ticker := time.NewTicker(m.cfg.Interval.Duration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, b := range m.pool.All() {
		wg.Add(1)
		go func(b *backend.Backend) {
			defer wg.Done()
			m.checkOne(ctx, b)
		}(b)
	}
	wg.Wait()
}

func (m *Monitor) checkOne(ctx context.Context, b *backend.Backend) {
	if b.State() == backend.StateDraining {
		return
	}

	err := m.checker.Check(ctx, b.Address, m.cfg.Timeout.Duration())

	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.states[b.Name]
	if !ok {
		c = &counters{}
		m.states[b.Name] = c
	}

	if err != nil {
		c.consecutiveSuccesses = 0
		c.consecutiveFailures++
		if c.consecutiveFailures >= m.cfg.UnhealthyThreshold && b.State() == backend.StateUp {
			b.SetState(backend.StateDown)
			m.logger.Warn("backend_down", "backend", b.Name, "address", b.Address, "error", err.Error())
		}
		return
	}

	c.consecutiveFailures = 0
	c.consecutiveSuccesses++
	if c.consecutiveSuccesses >= m.cfg.HealthyThreshold && b.State() == backend.StateDown {
		b.SetState(backend.StateUp)
		m.logger.Info("backend_up", "backend", b.Name, "address", b.Address)
	}
}
