package balancer

import (
	"context"
	"math/rand/v2"

	"github.com/milosursulovic/vortex/internal/backend"
)

// PowerOfTwoChoices samples two random healthy backends and picks whichever
// has fewer active connections, avoiding the cost of inspecting every
// backend on each request.
type PowerOfTwoChoices struct{}

func NewPowerOfTwoChoices() *PowerOfTwoChoices { return &PowerOfTwoChoices{} }

func (*PowerOfTwoChoices) Name() string { return "power_of_two_choices" }

func (*PowerOfTwoChoices) Next(_ context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	switch len(healthy) {
	case 0:
		return nil, ErrNoHealthyBackends
	case 1:
		return healthy[0], nil
	}

	i := rand.IntN(len(healthy))
	j := rand.IntN(len(healthy) - 1)
	if j >= i {
		j++
	}

	a, b := healthy[i], healthy[j]
	if a.ActiveConnections() <= b.ActiveConnections() {
		return a, nil
	}
	return b, nil
}
