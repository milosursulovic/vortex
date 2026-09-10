package admin

import (
	"sort"

	"github.com/milosursulovic/vortex/internal/backend"
)

// Registry looks up backends by name across every pool VORTEX built (the
// flat TCP pool, keyed "tcp", and each named HTTP backend_pool), for the
// admin API's backend endpoints. Backend names are globally unique (config
// validation enforces this), so a bare name unambiguously identifies one
// backend in one pool.
type Registry struct {
	pools map[string]*backend.Pool
}

func NewRegistry(pools map[string]*backend.Pool) *Registry {
	return &Registry{pools: pools}
}

// BackendInfo is a backend's admin-API-facing snapshot.
type BackendInfo struct {
	Pool                string `json:"pool"`
	Name                string `json:"name"`
	Address             string `json:"address"`
	Weight              int    `json:"weight"`
	State               string `json:"state"`
	ActiveConnections   int64  `json:"active_connections"`
	TotalConnections    int64  `json:"total_connections"`
	Failures            int64  `json:"failures"`
	CircuitBreakerState string `json:"circuit_breaker_state"`
}

// List returns every backend across every pool, ordered by pool then name.
func (r *Registry) List() []BackendInfo {
	out := make([]BackendInfo, 0)
	for poolName, pool := range r.pools {
		for _, b := range pool.All() {
			out = append(out, infoFor(poolName, b))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pool != out[j].Pool {
			return out[i].Pool < out[j].Pool
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Find looks up one backend by name across all pools.
func (r *Registry) Find(name string) (BackendInfo, bool) {
	for poolName, pool := range r.pools {
		if b, ok := pool.Get(name); ok {
			return infoFor(poolName, b), true
		}
	}
	return BackendInfo{}, false
}

// Enable, Disable, and Drain apply the corresponding backend.Pool lifecycle
// transition to whichever pool contains name, reporting whether it was
// found.
func (r *Registry) Enable(name string) bool  { return r.apply(name, (*backend.Pool).Enable) }
func (r *Registry) Disable(name string) bool { return r.apply(name, (*backend.Pool).Disable) }
func (r *Registry) Drain(name string) bool   { return r.apply(name, (*backend.Pool).Drain) }

func (r *Registry) apply(name string, fn func(*backend.Pool, string) bool) bool {
	for _, pool := range r.pools {
		if fn(pool, name) {
			return true
		}
	}
	return false
}

func infoFor(poolName string, b *backend.Backend) BackendInfo {
	return BackendInfo{
		Pool:                poolName,
		Name:                b.Name,
		Address:             b.Address,
		Weight:              b.Weight,
		State:               b.State().String(),
		ActiveConnections:   b.ActiveConnections(),
		TotalConnections:    b.TotalConnections(),
		Failures:            b.Failures(),
		CircuitBreakerState: b.Breaker().State().String(),
	}
}
