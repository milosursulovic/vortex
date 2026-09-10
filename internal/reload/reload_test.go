package reload

import (
	"log/slog"
	"testing"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestApplyAddsNewBackend(t *testing.T) {
	pool := backend.NewPool([]config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}})
	pools := map[string]*backend.Pool{"tcp": pool}

	cfg := &config.Config{
		Backends: []config.BackendConfig{
			{Name: "a", Address: "x:1", Weight: 1},
			{Name: "b", Address: "x:2", Weight: 1},
		},
	}

	Apply(cfg, pools, discardLogger())

	if len(pool.All()) != 2 {
		t.Fatalf("expected 2 backends after reload, got %d", len(pool.All()))
	}
	if _, ok := pool.Get("b"); !ok {
		t.Fatal("expected new backend b to be added")
	}
}

func TestApplyRemovesMissingBackend(t *testing.T) {
	pool := backend.NewPool([]config.BackendConfig{
		{Name: "a", Address: "x:1", Weight: 1},
		{Name: "b", Address: "x:2", Weight: 1},
	})
	pools := map[string]*backend.Pool{"tcp": pool}

	cfg := &config.Config{
		Backends: []config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}},
	}

	Apply(cfg, pools, discardLogger())

	if len(pool.All()) != 1 {
		t.Fatalf("expected 1 backend after reload, got %d", len(pool.All()))
	}
	if _, ok := pool.Get("b"); ok {
		t.Fatal("expected backend b to be removed")
	}
}

func TestApplyPreservesUnchangedBackendIdentity(t *testing.T) {
	pool := backend.NewPool([]config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}})
	original, _ := pool.Get("a")
	original.IncTotalConnections()

	pools := map[string]*backend.Pool{"tcp": pool}
	cfg := &config.Config{
		Backends: []config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}},
	}

	Apply(cfg, pools, discardLogger())

	after, _ := pool.Get("a")
	if after != original {
		t.Fatal("expected unchanged backend to keep the same object (preserving live counters)")
	}
	if after.TotalConnections() != 1 {
		t.Fatal("expected counters to survive a no-op reload")
	}
}

func TestApplyReplacesChangedBackend(t *testing.T) {
	pool := backend.NewPool([]config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}})
	original, _ := pool.Get("a")
	original.IncTotalConnections()

	pools := map[string]*backend.Pool{"tcp": pool}
	cfg := &config.Config{
		Backends: []config.BackendConfig{{Name: "a", Address: "x:2", Weight: 5}},
	}

	Apply(cfg, pools, discardLogger())

	after, _ := pool.Get("a")
	if after == original {
		t.Fatal("expected changed backend (address/weight) to be replaced")
	}
	if after.Address != "x:2" || after.Weight != 5 {
		t.Fatalf("expected updated address/weight, got %s weight %d", after.Address, after.Weight)
	}
	if after.TotalConnections() != 0 {
		t.Fatal("expected a replaced backend to start with fresh counters")
	}
}

func TestApplyIgnoresUnknownPool(t *testing.T) {
	pools := map[string]*backend.Pool{} // no "tcp" pool registered
	cfg := &config.Config{
		Backends: []config.BackendConfig{{Name: "a", Address: "x:1", Weight: 1}},
	}

	// Must not panic when no matching pool exists.
	Apply(cfg, pools, discardLogger())
}
