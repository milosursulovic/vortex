// Package integration exercises VORTEX's real internal packages wired
// together end to end, the way cmd/vortex assembles them, rather than unit
// testing one package in isolation.
package integration

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/balancer"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/metrics"
	"github.com/milosursulovic/vortex/internal/proxy"
	"github.com/milosursulovic/vortex/internal/router"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestConcurrentHTTPRequestsNoRaceNoLeak drives many concurrent requests
// through a real HTTPProxy (run this test with -race) and checks the
// things section 45 asks for: no failed requests, no negative counters,
// stats matching what actually happened, and zero leaked backend
// connections once the load finishes.
func TestConcurrentHTTPRequestsNoRaceNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy concurrency test in -short mode")
	}

	var served atomic.Int64
	backendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backendSrv.Close()

	pool := backend.NewPool([]config.BackendConfig{
		{Name: "b1", Address: backendSrv.Listener.Addr().String(), Weight: 1},
	})
	pool.ConfigureCircuitBreakers(config.CircuitBreakerConfig{
		Enabled: true, FailureThreshold: 5, OpenTimeout: config.Duration(time.Second),
	})

	bal, err := balancer.New("round_robin")
	if err != nil {
		t.Fatalf("balancer.New: %v", err)
	}

	r := router.New([]router.Route{{Host: "", Path: "/", Pool: pool, Balancer: bal}})
	rec, _ := metrics.NewRecorder()
	timeouts := config.TimeoutsConfig{
		Connect: config.Duration(2 * time.Second),
		Idle:    config.Duration(5 * time.Second),
	}
	hp := proxy.NewHTTPProxy(r, timeouts, config.LimitsConfig{}, nil, config.RetryConfig{}, rec, discardLogger())

	vortexSrv := httptest.NewServer(hp)
	defer vortexSrv.Close()

	const concurrency = 500
	const perGoroutine = 10
	wantTotal := int64(concurrency * perGoroutine)

	var wg sync.WaitGroup
	var errCount atomic.Int64
	client := &http.Client{Timeout: 5 * time.Second}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				resp, err := client.Get(vortexSrv.URL + "/")
				if err != nil {
					errCount.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if n := errCount.Load(); n > 0 {
		t.Fatalf("%d/%d requests failed", n, wantTotal)
	}
	if served.Load() != wantTotal {
		t.Fatalf("backend served %d requests, want %d", served.Load(), wantTotal)
	}

	snap := rec.Snapshot()
	if snap.Requests.Total != wantTotal {
		t.Fatalf("stats requests.total = %d, want %d", snap.Requests.Total, wantTotal)
	}
	if snap.Requests.Errors != 0 {
		t.Fatalf("stats requests.errors = %d, want 0", snap.Requests.Errors)
	}
	if snap.Connections.Active < 0 {
		t.Fatal("negative active connections after load")
	}

	b, _ := pool.Get("b1")
	if got := b.ActiveConnections(); got != 0 {
		t.Fatalf("expected 0 active backend connections once all requests finished, got %d", got)
	}
	if got := b.TotalConnections(); got != wantTotal {
		t.Fatalf("backend total_connections = %d, want %d", got, wantTotal)
	}
}
