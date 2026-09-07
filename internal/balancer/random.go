package balancer

import (
	"context"
	"math/rand/v2"

	"github.com/milosursulovic/vortex/internal/backend"
)

// Random picks a uniformly random healthy backend.
type Random struct{}

func NewRandom() *Random { return &Random{} }

func (*Random) Name() string { return "random" }

func (*Random) Next(_ context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}
	return healthy[rand.IntN(len(healthy))], nil
}
