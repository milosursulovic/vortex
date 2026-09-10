// Package reload applies a freshly re-read config to already-running
// backend pools, without touching listeners, TLS, or the load-balancing
// algorithm — those require a restart. Existing connections are left
// untouched; only backend pool membership and per-backend weight/address
// change, taking effect for new connections going forward.
package reload

import (
	"log/slog"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/circuitbreaker"
	"github.com/milosursulovic/vortex/internal/config"
)

// Apply reconciles every pool in pools (keyed "tcp" for the flat TCP pool,
// or a backend_pool name for HTTP) against cfg.
func Apply(cfg *config.Config, pools map[string]*backend.Pool, logger *slog.Logger) {
	if pool, ok := pools["tcp"]; ok {
		reconcile(pool, "tcp", cfg.Backends, cfg.CircuitBreaker, logger)
	}
	for _, pc := range cfg.BackendPools {
		if pool, ok := pools[pc.Name]; ok {
			reconcile(pool, pc.Name, pc.Backends, cfg.CircuitBreaker, logger)
		}
	}
}

func reconcile(pool *backend.Pool, poolName string, desired []config.BackendConfig, cbCfg config.CircuitBreakerConfig, logger *slog.Logger) {
	desiredByName := make(map[string]config.BackendConfig, len(desired))
	for _, b := range desired {
		desiredByName[b.Name] = b
	}

	for name, bc := range desiredByName {
		existing, exists := pool.Get(name)
		if exists && existing.Address == bc.Address && existing.Weight == bc.Weight {
			continue // unchanged, leave its live state/counters alone
		}

		nb := backend.New(bc.Name, bc.Address, bc.Weight)
		nb.ConfigureCircuitBreaker(circuitbreaker.New(cbCfg.Enabled, cbCfg.FailureThreshold, cbCfg.OpenTimeout.Duration()))
		pool.Add(nb) // Pool.Add replaces any existing same-named entry

		if exists {
			logger.Info("backend_updated_on_reload", "pool", poolName, "backend", name)
		} else {
			logger.Info("backend_added_on_reload", "pool", poolName, "backend", name)
		}
	}

	for _, existing := range pool.All() {
		if _, keep := desiredByName[existing.Name]; !keep {
			pool.Remove(existing.Name)
			logger.Info("backend_removed_on_reload", "pool", poolName, "backend", existing.Name)
		}
	}
}
