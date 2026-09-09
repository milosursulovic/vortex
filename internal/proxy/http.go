package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/router"
)

// HTTPProxy is VORTEX's Layer 7 reverse proxy: it routes each request by
// host/path to a backend pool, picks a backend via that pool's balancer,
// and forwards the request, rewriting forwarding headers along the way.
type HTTPProxy struct {
	router    *router.Router
	transport *trackingTransport
	logger    *slog.Logger
	reverse   *httputil.ReverseProxy
}

// NewHTTPProxy builds an HTTP proxy handler for r, dialing backends with
// timeouts.Connect and reusing idle backend connections per timeouts.Idle.
func NewHTTPProxy(r *router.Router, timeouts config.TimeoutsConfig, logger *slog.Logger) *HTTPProxy {
	transport := &trackingTransport{
		base: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: timeouts.Connect.Duration(),
			}).DialContext,
			IdleConnTimeout: timeouts.Idle.Duration(),
		},
	}

	p := &HTTPProxy{
		router:    r,
		transport: transport,
		logger:    logger,
	}
	p.reverse = &httputil.ReverseProxy{
		Rewrite:   p.rewrite,
		Transport: transport,
	}
	return p
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := p.router.Match(r.Host, r.URL.Path)
	if !ok {
		http.Error(w, "no matching route", http.StatusNotFound)
		return
	}

	ctx := common.WithClientAddr(r.Context(), r.RemoteAddr)
	target, err := route.Balancer.Next(ctx, route.Pool)
	if err != nil {
		p.logger.Error("backend_selection_failed", "host", r.Host, "path", r.URL.Path, "error", err.Error())
		http.Error(w, "no healthy backend", http.StatusServiceUnavailable)
		return
	}

	target.IncTotalConnections()
	p.logger.Info("backend_selected", "backend", target.Name, "address", target.Address, "host", r.Host, "path", r.URL.Path)

	p.reverse.ServeHTTP(w, r.WithContext(withSelectedBackend(ctx, target)))
}

// rewrite points the outgoing request at the backend chosen in ServeHTTP.
// httputil.ReverseProxy strips any inbound Forwarded/X-Forwarded-* headers
// before Rewrite runs, so SetXForwarded here sets them fresh from the
// actual connection rather than trusting client-supplied values.
func (p *HTTPProxy) rewrite(pr *httputil.ProxyRequest) {
	target, ok := selectedBackendFromContext(pr.In.Context())
	if !ok {
		return
	}

	pr.SetURL(&url.URL{Scheme: "http", Host: target.Address})
	pr.Out.Host = pr.In.Host // preserve the original Host header for the backend
	pr.SetXForwarded()
}

// trackingTransport wraps http.Transport to keep each backend's connection
// counters and failure counter in sync with actual round trips.
type trackingTransport struct {
	base *http.Transport
}

func (t *trackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := selectedBackendFromContext(req.Context())
	if !ok {
		return t.base.RoundTrip(req)
	}

	target.IncActiveConnections()
	defer target.DecActiveConnections()

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		target.IncFailures()
	}
	return resp, err
}

type backendCtxKeyType struct{}

var backendCtxKey = backendCtxKeyType{}

func withSelectedBackend(ctx context.Context, b *backend.Backend) context.Context {
	return context.WithValue(ctx, backendCtxKey, b)
}

func selectedBackendFromContext(ctx context.Context) (*backend.Backend, bool) {
	b, ok := ctx.Value(backendCtxKey).(*backend.Backend)
	return b, ok
}
