package admin

import (
	"testing"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/config"
)

func newTestRegistry() *Registry {
	tcpPool := backend.NewPool([]config.BackendConfig{{Name: "b1", Address: "x:1", Weight: 1}})
	httpPool := backend.NewPool([]config.BackendConfig{{Name: "web-1", Address: "x:2", Weight: 1}})
	return NewRegistry(map[string]*backend.Pool{"tcp": tcpPool, "frontend": httpPool})
}

func TestRegistryListIncludesAllPools(t *testing.T) {
	r := newTestRegistry()
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 backends across pools, got %d", len(list))
	}
}

func TestRegistryFindAcrossPools(t *testing.T) {
	r := newTestRegistry()

	info, ok := r.Find("web-1")
	if !ok {
		t.Fatal("expected to find web-1")
	}
	if info.Pool != "frontend" {
		t.Fatalf("expected pool frontend, got %q", info.Pool)
	}

	if _, ok := r.Find("does-not-exist"); ok {
		t.Fatal("expected not found for unknown backend")
	}
}

func TestRegistryEnableDisableDrain(t *testing.T) {
	r := newTestRegistry()

	if !r.Disable("b1") {
		t.Fatal("expected Disable to find b1")
	}
	info, _ := r.Find("b1")
	if info.State != "DOWN" {
		t.Fatalf("expected DOWN, got %s", info.State)
	}

	if !r.Drain("web-1") {
		t.Fatal("expected Drain to find web-1")
	}
	info, _ = r.Find("web-1")
	if info.State != "DRAINING" {
		t.Fatalf("expected DRAINING, got %s", info.State)
	}

	if !r.Enable("b1") {
		t.Fatal("expected Enable to find b1")
	}
	info, _ = r.Find("b1")
	if info.State != "UP" {
		t.Fatalf("expected UP, got %s", info.State)
	}

	if r.Enable("nope") {
		t.Fatal("expected Enable to report not-found for unknown backend")
	}
}
