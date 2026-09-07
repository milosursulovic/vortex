package health

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/config"
)

// fakeChecker lets tests control whether a check succeeds, and counts calls.
type fakeChecker struct {
	fail atomic.Bool
	n    atomic.Int64
}

func (f *fakeChecker) Check(_ context.Context, _ string, _ time.Duration) error {
	f.n.Add(1)
	if f.fail.Load() {
		return errors.New("simulated failure")
	}
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func newTestMonitor(t *testing.T, unhealthy, healthy int) (*Monitor, *backend.Pool, *fakeChecker) {
	t.Helper()
	pool := backend.NewPool([]config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}})
	checker := &fakeChecker{}
	m := &Monitor{
		pool:    pool,
		checker: checker,
		cfg: config.HealthCheckConfig{
			Enabled:            true,
			UnhealthyThreshold: unhealthy,
			HealthyThreshold:   healthy,
			Interval:           config.Duration(time.Millisecond),
			Timeout:            config.Duration(time.Second),
		},
		logger: discardLogger(),
		states: make(map[string]*counters),
	}
	return m, pool, checker
}

func TestMonitorMarksDownAfterConsecutiveFailures(t *testing.T) {
	m, pool, checker := newTestMonitor(t, 3, 2)
	checker.fail.Store(true)
	b, _ := pool.Get("a")

	m.checkOne(context.Background(), b)
	if b.State() != backend.StateUp {
		t.Fatal("should still be UP after 1 failure (threshold 3)")
	}
	m.checkOne(context.Background(), b)
	if b.State() != backend.StateUp {
		t.Fatal("should still be UP after 2 failures (threshold 3)")
	}
	m.checkOne(context.Background(), b)
	if b.State() != backend.StateDown {
		t.Fatal("should be DOWN after 3 consecutive failures")
	}
}

func TestMonitorDoesNotFlapOnSingleFailure(t *testing.T) {
	m, pool, checker := newTestMonitor(t, 3, 2)
	b, _ := pool.Get("a")

	checker.fail.Store(true)
	m.checkOne(context.Background(), b)
	checker.fail.Store(false)
	m.checkOne(context.Background(), b) // resets consecutive failures
	checker.fail.Store(true)
	m.checkOne(context.Background(), b)

	if b.State() != backend.StateUp {
		t.Fatal("a single isolated failure must not bring a backend down")
	}
}

func TestMonitorRecoversAfterConsecutiveSuccesses(t *testing.T) {
	m, pool, checker := newTestMonitor(t, 1, 2)
	b, _ := pool.Get("a")

	checker.fail.Store(true)
	m.checkOne(context.Background(), b)
	if b.State() != backend.StateDown {
		t.Fatal("expected DOWN after 1 failure (threshold 1)")
	}

	checker.fail.Store(false)
	m.checkOne(context.Background(), b)
	if b.State() != backend.StateDown {
		t.Fatal("should still be DOWN after 1 success (threshold 2)")
	}
	m.checkOne(context.Background(), b)
	if b.State() != backend.StateUp {
		t.Fatal("expected UP after 2 consecutive successes")
	}
}

func TestMonitorSkipsDrainingBackends(t *testing.T) {
	m, pool, checker := newTestMonitor(t, 1, 1)
	b, _ := pool.Get("a")
	pool.Drain("a")
	checker.fail.Store(true)

	m.checkOne(context.Background(), b)

	if checker.n.Load() != 0 {
		t.Fatal("draining backend should not be checked")
	}
	if b.State() != backend.StateDraining {
		t.Fatal("draining backend must not be moved to DOWN by health checks")
	}
}
