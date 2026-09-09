package router

import (
	"testing"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/balancer"
)

func routeFor(t *testing.T, host, path string) Route {
	t.Helper()
	return Route{
		Host:     host,
		Path:     path,
		Pool:     backend.NewPool(nil),
		Balancer: balancer.NewRoundRobin(),
	}
}

func TestMatchLongestPathPrefixWins(t *testing.T) {
	api := routeFor(t, "api.example.com", "/api")
	root := routeFor(t, "api.example.com", "/")
	r := New([]Route{root, api}) // deliberately reversed order

	got, ok := r.Match("api.example.com", "/api/users")
	if !ok {
		t.Fatal("expected a match")
	}
	if got.Path != "/api" {
		t.Fatalf("expected /api route (more specific), got %q", got.Path)
	}
}

func TestMatchFallsBackToCatchAll(t *testing.T) {
	api := routeFor(t, "api.example.com", "/api")
	root := routeFor(t, "api.example.com", "/")
	r := New([]Route{api, root})

	got, ok := r.Match("api.example.com", "/index.html")
	if !ok {
		t.Fatal("expected a match")
	}
	if got.Path != "/" {
		t.Fatalf("expected catch-all /, got %q", got.Path)
	}
}

func TestMatchHostSpecificOverWildcard(t *testing.T) {
	specific := routeFor(t, "app.example.com", "/")
	wildcard := routeFor(t, "", "/")
	r := New([]Route{wildcard, specific})

	got, ok := r.Match("app.example.com", "/")
	if !ok {
		t.Fatal("expected a match")
	}
	if got.Host != "app.example.com" {
		t.Fatalf("expected host-specific route to win over wildcard, got host %q", got.Host)
	}
}

func TestMatchStripsPortFromHost(t *testing.T) {
	api := routeFor(t, "api.example.com", "/")
	r := New([]Route{api})

	got, ok := r.Match("api.example.com:8080", "/")
	if !ok {
		t.Fatal("expected host:port to match host-only route")
	}
	if got.Path != "/" {
		t.Fatalf("unexpected route %+v", got)
	}
}

func TestMatchNoRoute(t *testing.T) {
	r := New([]Route{routeFor(t, "api.example.com", "/api")})

	if _, ok := r.Match("other.example.com", "/api"); ok {
		t.Fatal("expected no match for unrelated host")
	}
}
