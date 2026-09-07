package backend

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/milosursulovic/vortex/internal/config"
)

// Pool is the registry of backends VORTEX can proxy to.
type Pool struct {
	mu       sync.RWMutex
	byName   map[string]*Backend
	order    []*Backend // stable iteration order for round-robin
	rrCursor atomic.Uint64
}

// NewPool builds a pool from the configured backends, each starting UP.
func NewPool(backends []config.BackendConfig) *Pool {
	p := &Pool{byName: make(map[string]*Backend, len(backends))}
	for _, b := range backends {
		p.Add(New(b.Name, b.Address, b.Weight))
	}
	return p
}

// Add registers a backend, replacing any existing one with the same name.
func (p *Pool) Add(b *Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.byName[b.Name]; exists {
		p.removeLocked(b.Name)
	}
	p.byName[b.Name] = b
	p.order = append(p.order, b)
}

// Remove unregisters a backend by name. It reports whether one was found.
func (p *Pool) Remove(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.removeLocked(name)
}

func (p *Pool) removeLocked(name string) bool {
	if _, exists := p.byName[name]; !exists {
		return false
	}
	delete(p.byName, name)
	for i, b := range p.order {
		if b.Name == name {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	return true
}

// Get returns the backend registered under name, if any.
func (p *Pool) Get(name string) (*Backend, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	b, ok := p.byName[name]
	return b, ok
}

// All returns every registered backend, in registration order.
func (p *Pool) All() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Backend, len(p.order))
	copy(out, p.order)
	return out
}

// Enable transitions a backend to UP, accepting new connections again.
func (p *Pool) Enable(name string) bool {
	b, ok := p.Get(name)
	if !ok {
		return false
	}
	b.SetState(StateUp)
	return true
}

// Disable immediately transitions a backend to DOWN.
func (p *Pool) Disable(name string) bool {
	b, ok := p.Get(name)
	if !ok {
		return false
	}
	b.SetState(StateDown)
	return true
}

// Drain transitions a backend to DRAINING: it stops receiving new
// connections but existing ones are left to finish on their own.
func (p *Pool) Drain(name string) bool {
	b, ok := p.Get(name)
	if !ok {
		return false
	}
	b.SetState(StateDraining)
	return true
}

// Healthy returns every UP backend, in registration order.
func (p *Pool) Healthy() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]*Backend, 0, len(p.order))
	for _, b := range p.order {
		if b.State() == StateUp {
			out = append(out, b)
		}
	}
	return out
}

// Next picks the next UP backend in round-robin order.
func (p *Pool) Next() (*Backend, error) {
	healthy := p.Healthy()
	if len(healthy) == 0 {
		return nil, fmt.Errorf("no healthy backends available")
	}
	i := p.rrCursor.Add(1) - 1
	return healthy[i%uint64(len(healthy))], nil
}
