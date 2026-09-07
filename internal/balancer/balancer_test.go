package balancer

import (
	"context"
	"testing"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
	"github.com/milosursulovic/vortex/internal/config"
)

func newTestPool(t *testing.T, backends ...config.BackendConfig) *backend.Pool {
	t.Helper()
	return backend.NewPool(backends)
}

func TestRoundRobinCyclesInOrder(t *testing.T) {
	pool := newTestPool(t,
		config.BackendConfig{Name: "a", Address: "x", Weight: 1},
		config.BackendConfig{Name: "b", Address: "x", Weight: 1},
	)
	rr := NewRoundRobin()

	var got []string
	for i := 0; i < 4; i++ {
		b, err := rr.Next(context.Background(), pool)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, b.Name)
	}

	want := []string{"a", "b", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRoundRobinErrorsWhenNoneHealthy(t *testing.T) {
	pool := newTestPool(t, config.BackendConfig{Name: "a", Address: "x", Weight: 1})
	pool.Disable("a")

	if _, err := NewRoundRobin().Next(context.Background(), pool); err != ErrNoHealthyBackends {
		t.Fatalf("expected ErrNoHealthyBackends, got %v", err)
	}
}

func TestWeightedRoundRobinFollowsWeights(t *testing.T) {
	// Matches the spec's worked example: weights 5/2/1 over an 8-cycle
	// produce five A's, two B's, then one C before repeating.
	pool := newTestPool(t,
		config.BackendConfig{Name: "a", Address: "x", Weight: 5},
		config.BackendConfig{Name: "b", Address: "x", Weight: 2},
		config.BackendConfig{Name: "c", Address: "x", Weight: 1},
	)
	wrr := NewWeightedRoundRobin()

	var got []string
	for i := 0; i < 8; i++ {
		b, err := wrr.Next(context.Background(), pool)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, b.Name)
	}

	want := []string{"a", "a", "a", "a", "a", "b", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestLeastConnectionsPicksFewestActive(t *testing.T) {
	pool := newTestPool(t,
		config.BackendConfig{Name: "a", Address: "x", Weight: 1},
		config.BackendConfig{Name: "b", Address: "x", Weight: 1},
	)
	a, _ := pool.Get("a")
	a.IncActiveConnections()
	a.IncActiveConnections()

	lc := NewLeastConnections()
	b, err := lc.Next(context.Background(), pool)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if b.Name != "b" {
		t.Fatalf("expected b (fewer active connections), got %s", b.Name)
	}
}

func TestIPHashIsDeterministicPerClient(t *testing.T) {
	pool := newTestPool(t,
		config.BackendConfig{Name: "a", Address: "x", Weight: 1},
		config.BackendConfig{Name: "b", Address: "x", Weight: 1},
		config.BackendConfig{Name: "c", Address: "x", Weight: 1},
	)
	h := NewIPHash()
	ctx := common.WithClientAddr(context.Background(), "203.0.113.7:54321")

	first, err := h.Next(ctx, pool)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := h.Next(ctx, pool)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if again.Name != first.Name {
			t.Fatalf("expected same backend %s for same client, got %s", first.Name, again.Name)
		}
	}
}

func TestConsistentHashingIsDeterministicPerClient(t *testing.T) {
	pool := newTestPool(t,
		config.BackendConfig{Name: "a", Address: "x", Weight: 1},
		config.BackendConfig{Name: "b", Address: "x", Weight: 1},
		config.BackendConfig{Name: "c", Address: "x", Weight: 1},
	)
	ch := NewConsistentHashing()
	ctx := common.WithClientAddr(context.Background(), "203.0.113.7:54321")

	first, err := ch.Next(ctx, pool)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := ch.Next(ctx, pool)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if again.Name != first.Name {
			t.Fatalf("expected same backend %s for same client, got %s", first.Name, again.Name)
		}
	}
}

func TestFactoryUnknownAlgorithm(t *testing.T) {
	if _, err := New("does_not_exist"); err == nil {
		t.Fatal("expected error for unknown algorithm")
	}
}

func TestFactoryKnownAlgorithms(t *testing.T) {
	for _, name := range []string{
		"", "round_robin", "weighted_round_robin", "random",
		"least_connections", "power_of_two_choices", "ip_hash", "consistent_hashing",
	} {
		if _, err := New(name); err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
	}
}
