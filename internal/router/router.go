// Package router maps an incoming HTTP request's host and path to the
// backend pool (and its balancer) that should handle it.
package router

import (
	"net"
	"sort"
	"strings"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/balancer"
)

// Route binds a host/path prefix match to a pool and the balancer that
// picks among its backends.
type Route struct {
	Host     string // "" or "*" matches any host
	Path     string // prefix match
	Pool     *backend.Pool
	Balancer balancer.Balancer
}

// Router holds routes ordered so the most specific (longest path) match
// wins regardless of input order.
type Router struct {
	routes []Route
}

// New builds a Router, ordering routes so a host-specific route always
// beats a host-wildcard one, and within the same host-specificity a
// longer (more specific) path prefix beats a shorter catch-all like "/".
func New(routes []Route) *Router {
	sorted := make([]Route, len(routes))
	copy(sorted, routes)
	sort.SliceStable(sorted, func(i, j int) bool {
		iSpecific, jSpecific := isSpecificHost(sorted[i].Host), isSpecificHost(sorted[j].Host)
		if iSpecific != jSpecific {
			return iSpecific
		}
		return len(sorted[i].Path) > len(sorted[j].Path)
	})
	return &Router{routes: sorted}
}

func isSpecificHost(host string) bool {
	return host != "" && host != "*"
}

// Match finds the first (most specific) route whose host and path prefix
// match the request.
func (r *Router) Match(host, path string) (*Route, bool) {
	host = stripPort(host)
	for i := range r.routes {
		rt := &r.routes[i]
		if !hostMatches(rt.Host, host) {
			continue
		}
		if !strings.HasPrefix(path, rt.Path) {
			continue
		}
		return rt, true
	}
	return nil, false
}

func hostMatches(routeHost, requestHost string) bool {
	return routeHost == "" || routeHost == "*" || strings.EqualFold(routeHost, requestHost)
}

func stripPort(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	return h
}
