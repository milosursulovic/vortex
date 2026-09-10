package integration

import (
	"bytes"
	"crypto/rand"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/balancer"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/limits"
	"github.com/milosursulovic/vortex/internal/listener"
	"github.com/milosursulovic/vortex/internal/metrics"
)

// startEchoServer runs a plain TCP echo server for the duration of the
// test, returning its address.
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()

	return ln.Addr().String()
}

// TestConcurrentTCPRoundTripsNoCorruptionNoLeak sends many concurrent
// byte-exact round trips through the real TCP proxy path (listener.Manager
// + proxy.ServeTCP), including a client-side half-close mid-stream, to
// guard against the truncation bug fixed in this phase (closing both legs
// as soon as either direction finished) regressing, and to check for
// leaked active connections under concurrency (run with -race).
func TestConcurrentTCPRoundTripsNoCorruptionNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy concurrency test in -short mode")
	}

	echoAddr := startEchoServer(t)

	pool := backend.NewPool([]config.BackendConfig{{Name: "b1", Address: echoAddr, Weight: 1}})
	pool.ConfigureCircuitBreakers(config.CircuitBreakerConfig{
		Enabled: true, FailureThreshold: 5, OpenTimeout: config.Duration(time.Second),
	})
	bal, err := balancer.New("round_robin")
	if err != nil {
		t.Fatalf("balancer.New: %v", err)
	}
	picker := &balancer.Picker{Balancer: bal, Pool: pool}

	rec, _ := metrics.NewRecorder()
	mgr := listener.NewManager(discardLogger(), limits.NewConnLimiter(0), rec)

	timeouts := config.TimeoutsConfig{
		Connect: config.Duration(2 * time.Second),
		Read:    config.Duration(5 * time.Second),
		Write:   config.Duration(5 * time.Second),
		Idle:    config.Duration(5 * time.Second),
	}
	listenerCfg := config.ListenerConfig{Name: "tcp", Address: "127.0.0.1:0", Protocol: "tcp"}

	// listener.Manager binds its own address; grab it back out to dial.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxyAddr := ln.Addr().String()
	ln.Close()
	listenerCfg.Address = proxyAddr

	if err := mgr.StartTCP(listenerCfg, picker, timeouts, config.LimitsConfig{}); err != nil {
		t.Fatalf("StartTCP: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })

	const concurrency = 300
	payload := make([]byte, 64*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	var wg sync.WaitGroup
	var mismatches atomic.Int64
	var dialErrs atomic.Int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
			if err != nil {
				dialErrs.Add(1)
				return
			}
			defer conn.Close()

			if _, err := conn.Write(payload); err != nil {
				mismatches.Add(1)
				return
			}
			// Half-close: done sending, but still expect the full echo back.
			if tc, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = tc.CloseWrite()
			}

			got := make([]byte, 0, len(payload))
			buf := make([]byte, 32*1024)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for len(got) < len(payload) {
				n, err := conn.Read(buf)
				got = append(got, buf[:n]...)
				if err != nil {
					break
				}
			}

			if !bytes.Equal(got, payload) {
				mismatches.Add(1)
				t.Logf("goroutine %d: got %d bytes, want %d", i, len(got), len(payload))
			}
		}(i)
	}
	wg.Wait()

	if n := dialErrs.Load(); n > 0 {
		t.Fatalf("%d/%d connections failed to dial", n, concurrency)
	}
	if n := mismatches.Load(); n > 0 {
		t.Fatalf("%d/%d connections got truncated/corrupted echoes", n, concurrency)
	}

	// Give the proxy's own goroutines a moment to finish their bookkeeping
	// after the client side has already observed EOF.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if rec.ConnectionsActive.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leaked connections: ConnectionsActive = %d after all clients finished", rec.ConnectionsActive.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := rec.ConnectionsTotal.Load(); got != int64(concurrency) {
		t.Fatalf("ConnectionsTotal = %d, want %d", got, concurrency)
	}
	b, _ := pool.Get("b1")
	if got := b.ActiveConnections(); got != 0 {
		t.Fatalf("backend active_connections = %d, want 0", got)
	}
}
