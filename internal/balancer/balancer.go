// Package balancer implements pluggable backend-selection strategies on top
// of a backend.Pool.
package balancer

import (
	"context"
	"errors"

	"github.com/milosursulovic/vortex/internal/backend"
)

// ErrNoHealthyBackends is returned when a pool has no UP backend to pick.
var ErrNoHealthyBackends = errors.New("no healthy backends available")

// Balancer selects the next backend to route a connection to.
type Balancer interface {
	Name() string
	Next(ctx context.Context, pool *backend.Pool) (*backend.Backend, error)
}

// Picker adapts a Balancer bound to a specific Pool into whatever narrower
// interface a caller (e.g. the proxy layer) expects.
type Picker struct {
	Balancer Balancer
	Pool     *backend.Pool
}

func (p *Picker) Next(ctx context.Context) (*backend.Backend, error) {
	return p.Balancer.Next(ctx, p.Pool)
}
