package balancer

import (
	"context"
	"sync/atomic"

	"github.com/milosursulovic/vortex/internal/backend"
)

// RoundRobin cycles through the healthy backends in order.
type RoundRobin struct {
	cursor atomic.Uint64
}

func NewRoundRobin() *RoundRobin { return &RoundRobin{} }

func (*RoundRobin) Name() string { return "round_robin" }

func (r *RoundRobin) Next(_ context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}
	i := r.cursor.Add(1) - 1
	return healthy[i%uint64(len(healthy))], nil
}
