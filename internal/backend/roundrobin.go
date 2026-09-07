// Package backend holds backend targets and the (initially simple) picking
// logic used by the TCP proxy. This is deliberately minimal for the Phase 2
// TCP proxy; it is replaced by a full BackendPool with health/state tracking
// in a later phase and a pluggable Balancer interface after that.
package backend

import (
	"fmt"
	"sync/atomic"

	"github.com/milosursulovic/vortex/internal/config"
)

// Target is a single proxyable backend address.
type Target struct {
	Name    string
	Address string
}

// RoundRobin cycles through a fixed list of targets.
type RoundRobin struct {
	targets []Target
	next    atomic.Uint64
}

// NewRoundRobin builds a picker from configured backends.
func NewRoundRobin(backends []config.BackendConfig) *RoundRobin {
	targets := make([]Target, len(backends))
	for i, b := range backends {
		targets[i] = Target{Name: b.Name, Address: b.Address}
	}
	return &RoundRobin{targets: targets}
}

// Next returns the next target in rotation, or an error if none are configured.
func (r *RoundRobin) Next() (Target, error) {
	if len(r.targets) == 0 {
		return Target{}, fmt.Errorf("no backends configured")
	}
	i := r.next.Add(1) - 1
	return r.targets[i%uint64(len(r.targets))], nil
}
