// Package backend holds the backend pool: registry, health/lifecycle state,
// and per-backend connection counters. Load-balancing strategy is a
// separate concern (added on top of the pool in a later phase); today the
// pool itself hands out backends in a simple round-robin over healthy ones.
package backend

import "sync/atomic"

// State is a backend's position in its lifecycle.
type State int32

const (
	StateUp State = iota
	StateDraining
	StateDown
)

func (s State) String() string {
	switch s {
	case StateUp:
		return "UP"
	case StateDraining:
		return "DRAINING"
	case StateDown:
		return "DOWN"
	default:
		return "UNKNOWN"
	}
}

// Backend is one proxyable target and its live state.
type Backend struct {
	Name    string
	Address string
	Weight  int

	state             atomic.Int32
	activeConnections atomic.Int64
	totalConnections  atomic.Int64
	failures          atomic.Int64
}

// New creates a Backend in StateUp.
func New(name, address string, weight int) *Backend {
	b := &Backend{Name: name, Address: address, Weight: weight}
	b.state.Store(int32(StateUp))
	return b
}

func (b *Backend) State() State     { return State(b.state.Load()) }
func (b *Backend) SetState(s State) { b.state.Store(int32(s)) }

func (b *Backend) IncActiveConnections() int64 { return b.activeConnections.Add(1) }
func (b *Backend) DecActiveConnections() int64 { return b.activeConnections.Add(-1) }
func (b *Backend) ActiveConnections() int64    { return b.activeConnections.Load() }

func (b *Backend) IncTotalConnections()    { b.totalConnections.Add(1) }
func (b *Backend) TotalConnections() int64 { return b.totalConnections.Load() }

func (b *Backend) IncFailures()    { b.failures.Add(1) }
func (b *Backend) Failures() int64 { return b.failures.Load() }
