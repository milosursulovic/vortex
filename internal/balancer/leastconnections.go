package balancer

import (
	"context"

	"github.com/milosursulovic/vortex/internal/backend"
)

// LeastConnections picks the healthy backend with the fewest active
// connections.
type LeastConnections struct{}

func NewLeastConnections() *LeastConnections { return &LeastConnections{} }

func (*LeastConnections) Name() string { return "least_connections" }

func (*LeastConnections) Next(_ context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}

	best := healthy[0]
	for _, b := range healthy[1:] {
		if b.ActiveConnections() < best.ActiveConnections() {
			best = b
		}
	}
	return best, nil
}
