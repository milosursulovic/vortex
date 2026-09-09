package proxy

import (
	"context"
	"fmt"

	"github.com/milosursulovic/vortex/internal/backend"
)

// maxBackendSelectionAttempts bounds how many times we ask the balancer for
// a different backend before giving up, when the first choice is
// unavailable (circuit breaker open, or at its per-backend connection cap).
const maxBackendSelectionAttempts = 5

// selectAvailableBackend asks picker for a backend, skipping ones whose
// circuit breaker is open or that are already at maxPerBackend active
// connections (0 = no per-backend cap), up to a bounded number of tries.
func selectAvailableBackend(ctx context.Context, picker BackendPicker, maxPerBackend int) (*backend.Backend, error) {
	var lastErr error

	for i := 0; i < maxBackendSelectionAttempts; i++ {
		b, err := picker.Next(ctx)
		if err != nil {
			return nil, err
		}

		if !b.Breaker().Allow() {
			lastErr = fmt.Errorf("backend %s: circuit breaker open", b.Name)
			continue
		}
		if maxPerBackend > 0 && b.ActiveConnections() >= int64(maxPerBackend) {
			lastErr = fmt.Errorf("backend %s: at max connections per backend (%d)", b.Name, maxPerBackend)
			continue
		}

		return b, nil
	}

	return nil, lastErr
}
