package balancer

import (
	"context"
	"sync/atomic"

	"github.com/milosursulovic/vortex/internal/backend"
)

// WeightedRoundRobin cycles through healthy backends proportionally to
// weight: a backend with weight 5 receives 5 consecutive turns out of every
// total-weight-sized cycle before moving to the next backend.
type WeightedRoundRobin struct {
	cursor atomic.Uint64
}

func NewWeightedRoundRobin() *WeightedRoundRobin { return &WeightedRoundRobin{} }

func (*WeightedRoundRobin) Name() string { return "weighted_round_robin" }

func (w *WeightedRoundRobin) Next(_ context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}

	totalWeight := 0
	for _, b := range healthy {
		totalWeight += effectiveWeight(b)
	}

	idx := int(w.cursor.Add(1)-1) % totalWeight
	for _, b := range healthy {
		wt := effectiveWeight(b)
		if idx < wt {
			return b, nil
		}
		idx -= wt
	}

	// Unreachable: idx is always < totalWeight by construction above.
	return healthy[0], nil
}

func effectiveWeight(b *backend.Backend) int {
	if b.Weight <= 0 {
		return 1
	}
	return b.Weight
}
