package backend

import (
	"testing"

	"github.com/milosursulovic/vortex/internal/config"
)

func newTestPool() *Pool {
	return NewPool([]config.BackendConfig{
		{Name: "a", Address: "127.0.0.1:1", Weight: 1},
		{Name: "b", Address: "127.0.0.1:2", Weight: 1},
	})
}

func TestPoolNextRoundRobinsOverHealthy(t *testing.T) {
	p := newTestPool()

	var got []string
	for i := 0; i < 4; i++ {
		b, err := p.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, b.Name)
	}

	want := []string{"a", "b", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPoolNextSkipsNonUpBackends(t *testing.T) {
	p := newTestPool()
	p.Disable("a")

	for i := 0; i < 3; i++ {
		b, err := p.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if b.Name != "b" {
			t.Fatalf("expected only healthy backend b, got %s", b.Name)
		}
	}
}

func TestPoolNextErrorsWhenNoneHealthy(t *testing.T) {
	p := newTestPool()
	p.Disable("a")
	p.Disable("b")

	if _, err := p.Next(); err == nil {
		t.Fatal("expected error when no backends are healthy")
	}
}

func TestPoolDrainExcludesFromNewConnectionsButKeepsRegistered(t *testing.T) {
	p := newTestPool()
	p.Drain("a")

	b, ok := p.Get("a")
	if !ok {
		t.Fatal("drained backend should remain registered")
	}
	if b.State() != StateDraining {
		t.Fatalf("expected DRAINING, got %s", b.State())
	}

	for i := 0; i < 3; i++ {
		next, err := p.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if next.Name == "a" {
			t.Fatal("draining backend must not receive new connections")
		}
	}
}

func TestPoolEnableRestoresBackend(t *testing.T) {
	p := newTestPool()
	p.Disable("a")
	p.Enable("a")

	b, _ := p.Get("a")
	if b.State() != StateUp {
		t.Fatalf("expected UP after Enable, got %s", b.State())
	}
}

func TestPoolRemove(t *testing.T) {
	p := newTestPool()
	if !p.Remove("a") {
		t.Fatal("expected Remove to report found")
	}
	if _, ok := p.Get("a"); ok {
		t.Fatal("removed backend should no longer be registered")
	}
	if len(p.All()) != 1 {
		t.Fatalf("expected 1 remaining backend, got %d", len(p.All()))
	}
}
