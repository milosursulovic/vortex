package balancer

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
	"github.com/milosursulovic/vortex/internal/config"
)

func newBenchPool(n int) *backend.Pool {
	backends := make([]config.BackendConfig, n)
	for i := range backends {
		backends[i] = config.BackendConfig{
			Name:    fmt.Sprintf("b%d", i),
			Address: fmt.Sprintf("10.0.0.%d:8080", i%255),
			Weight:  (i % 5) + 1,
		}
	}
	return backend.NewPool(backends)
}

// benchmarkBalancer runs bal.Next concurrently (matching how the proxy
// actually calls it, from many goroutines at once) against a pool of
// poolSize backends, each call carrying a distinct simulated client IP so
// IP-hash-style algorithms do real hashing work instead of hitting a
// trivial fallback path.
func benchmarkBalancer(b *testing.B, newBal func() Balancer, poolSize int) {
	pool := newBenchPool(poolSize)
	bal := newBal()

	var counter atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := counter.Add(1)
			ip := fmt.Sprintf("203.0.113.%d:%d", n%255, 40000+n%1000)
			ctx := common.WithClientAddr(context.Background(), ip)
			if _, err := bal.Next(ctx, pool); err != nil {
				b.Fatal(err)
			}
		}
	})
}

var poolSizes = []int{3, 10, 100}

func BenchmarkRoundRobin(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewRoundRobin() }, n)
		})
	}
}

func BenchmarkWeightedRoundRobin(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewWeightedRoundRobin() }, n)
		})
	}
}

func BenchmarkRandom(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewRandom() }, n)
		})
	}
}

func BenchmarkLeastConnections(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewLeastConnections() }, n)
		})
	}
}

func BenchmarkPowerOfTwoChoices(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewPowerOfTwoChoices() }, n)
		})
	}
}

func BenchmarkIPHash(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewIPHash() }, n)
		})
	}
}

func BenchmarkConsistentHashing(b *testing.B) {
	for _, n := range poolSizes {
		b.Run(fmt.Sprintf("pool%d", n), func(b *testing.B) {
			benchmarkBalancer(b, func() Balancer { return NewConsistentHashing() }, n)
		})
	}
}
