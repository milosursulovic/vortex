package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/balancer"
	"github.com/milosursulovic/vortex/internal/common"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/metrics"
	"github.com/milosursulovic/vortex/internal/ratelimit"
	"github.com/milosursulovic/vortex/internal/router"
	"github.com/milosursulovic/vortex/internal/tracing"
)

// HTTPProxy is VORTEX's Layer 7 reverse proxy: it rate-limits, routes each
// request by host/path to a backend pool, picks a backend via that pool's
// balancer, and forwards the request, rewriting forwarding headers,
// enforcing resource limits, and retrying safe methods on transport
// failure along the way.
type HTTPProxy struct {
	router      *router.Router
	transport   *trackingTransport
	logger      *slog.Logger
	reverse     *httputil.ReverseProxy
	limits      config.LimitsConfig
	rateLimiter *ratelimit.Limiter // nil disables rate limiting
	retry       config.RetryConfig
	rec         *metrics.Recorder
}

// NewHTTPProxy builds an HTTP proxy handler for r, dialing backends with
// timeouts.Connect and reusing idle backend connections per timeouts.Idle.
// rateLimiter may be nil to disable rate limiting.
func NewHTTPProxy(r *router.Router, timeouts config.TimeoutsConfig, limitsCfg config.LimitsConfig, rateLimiter *ratelimit.Limiter, retryCfg config.RetryConfig, rec *metrics.Recorder, logger *slog.Logger) *HTTPProxy {
	transport := &trackingTransport{
		base: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: timeouts.Connect.Duration(),
			}).DialContext,
			IdleConnTimeout: timeouts.Idle.Duration(),
		},
		rec: rec,
	}

	p := &HTTPProxy{
		router:      r,
		transport:   transport,
		logger:      logger,
		limits:      limitsCfg,
		rateLimiter: rateLimiter,
		retry:       retryCfg,
		rec:         rec,
	}
	p.reverse = &httputil.ReverseProxy{
		Rewrite:      p.rewrite,
		Transport:    transport,
		ErrorHandler: p.errorHandler,
	}
	return p
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracing.Tracer().Start(r.Context(), "vortex.http.request", trace.WithAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.host", r.Host),
		attribute.String("http.path", r.URL.Path),
	))
	defer span.End()
	r = r.WithContext(ctx)

	start := time.Now()
	p.rec.RequestsTotal.Add(1)

	cw := &countingResponseWriter{ResponseWriter: w}
	cr := &countingReadCloser{ReadCloser: r.Body}
	r.Body = cr
	defer func() {
		p.rec.BytesReceived.Add(cr.n)
		p.rec.BytesSent.Add(cw.n)
		p.rec.ObserveRequestDuration(time.Since(start))
	}()

	clientIP := clientHost(r.RemoteAddr)

	if p.rateLimiter != nil && !p.rateLimiter.Allow(clientIP) {
		span.SetStatus(codes.Error, "rate limit exceeded")
		http.Error(cw, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	route, ok := p.router.Match(r.Host, r.URL.Path)
	if !ok {
		span.SetStatus(codes.Error, "no matching route")
		http.Error(cw, "no matching route", http.StatusNotFound)
		return
	}
	span.SetAttributes(
		attribute.String("vortex.route.host", route.Host),
		attribute.String("vortex.route.path", route.Path),
	)

	if p.limits.MaxRequestBodyMB > 0 {
		r.Body = http.MaxBytesReader(cw, r.Body, int64(p.limits.MaxRequestBodyMB)*1024*1024)
	}

	ctx = common.WithClientAddr(r.Context(), r.RemoteAddr)
	picker := &balancer.Picker{Balancer: route.Balancer, Pool: route.Pool}
	target, err := selectAvailableBackend(ctx, picker, p.limits.MaxConnectionsPerBackend)
	if err != nil {
		p.rec.ErrorsTotal.Add(1)
		span.RecordError(err)
		span.SetStatus(codes.Error, "no healthy backend")
		p.logger.Error("backend_selection_failed", "host", r.Host, "path", r.URL.Path, "error", err.Error())
		http.Error(cw, "no healthy backend", http.StatusServiceUnavailable)
		return
	}

	target.IncTotalConnections()
	span.SetAttributes(attribute.String("vortex.backend", target.Name))
	p.logger.Info("backend_selected", "backend", target.Name, "address", target.Address, "host", r.Host, "path", r.URL.Path)

	ctx = withSelectedBackend(ctx, target)
	ctx = withRoute(ctx, route)
	p.reverse.ServeHTTP(cw, r.WithContext(ctx))
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

	// Propagate trace context to the backend via standard W3C traceparent
	// headers, so an instrumented backend can continue the same trace.
	otel.GetTextMapPropagator().Inject(pr.Out.Context(), propagation.HeaderCarrier(pr.Out.Header))
}

// errorHandler runs when the round trip to the backend itself failed
// (connection refused, timeout, ...) before any response was written to
// the client. If retrying is enabled, the method is safe to retry (GET,
// HEAD, OPTIONS — never a method that may have already sent a body), and
// the failure class is one retry.RetryOn allows, it picks a fresh backend
// and tries again, up to retry.MaxRetries times.
func (p *HTTPProxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	attempts, _ := retryAttemptsFromContext(r.Context())

	if p.retry.Enabled &&
		isRetryableMethod(r.Method) &&
		attempts < p.retry.MaxRetries &&
		slices.Contains(p.retry.RetryOn, classifyError(err)) {

		route, ok := routeFromContext(r.Context())
		if ok {
			p.logger.Warn("retrying_request", "attempt", attempts+1, "host", r.Host, "path", r.URL.Path, "error", err.Error())

			ctx := common.WithClientAddr(r.Context(), r.RemoteAddr)
			picker := &balancer.Picker{Balancer: route.Balancer, Pool: route.Pool}
			target, selErr := selectAvailableBackend(ctx, picker, p.limits.MaxConnectionsPerBackend)
			if selErr == nil {
				target.IncTotalConnections()
				ctx = withSelectedBackend(ctx, target)
				ctx = withRoute(ctx, route)
				ctx = withRetryAttempts(ctx, attempts+1)
				p.reverse.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
	}

	p.rec.ErrorsTotal.Add(1)
	if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backend unavailable")
	}
	p.logger.Error("proxy_error", "host", r.Host, "path", r.URL.Path, "error", err.Error())
	http.Error(w, "backend unavailable", http.StatusBadGateway)
}

func isRetryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// classifyError buckets a transport error into the coarse categories
// retry.retry_on is configured against.
func classifyError(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "connection_failure"
}

// trackingTransport wraps http.Transport to keep each backend's connection
// counters, failure counter, and circuit breaker in sync with actual round
// trips.
type trackingTransport struct {
	base *http.Transport
	rec  *metrics.Recorder
}

func (t *trackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := selectedBackendFromContext(req.Context())
	if !ok {
		return t.base.RoundTrip(req)
	}

	ctx, span := tracing.Tracer().Start(req.Context(), "vortex.backend.roundtrip", trace.WithAttributes(
		attribute.String("vortex.backend", target.Name),
		attribute.String("vortex.backend.address", target.Address),
	))
	defer span.End()
	req = req.WithContext(ctx)

	target.IncActiveConnections()
	defer target.DecActiveConnections()

	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	t.rec.ObserveBackendLatency(target.Name, time.Since(start))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		target.IncFailures()
		target.Breaker().RecordFailure()
	} else {
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		target.Breaker().RecordSuccess()
		t.rec.ResponsesTotal.Add(1)
	}
	return resp, err
}

// countingResponseWriter tracks bytes written to the client.
type countingResponseWriter struct {
	http.ResponseWriter
	n int64
}

func (w *countingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}

// countingReadCloser tracks bytes read from the client request body.
type countingReadCloser struct {
	io.ReadCloser
	n int64
}

func (r *countingReadCloser) Read(b []byte) (int, error) {
	n, err := r.ReadCloser.Read(b)
	r.n += int64(n)
	return n, err
}

func clientHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

type backendCtxKeyType struct{}
type routeCtxKeyType struct{}
type retryAttemptsCtxKeyType struct{}

var (
	backendCtxKey       = backendCtxKeyType{}
	routeCtxKey         = routeCtxKeyType{}
	retryAttemptsCtxKey = retryAttemptsCtxKeyType{}
)

func withSelectedBackend(ctx context.Context, b *backend.Backend) context.Context {
	return context.WithValue(ctx, backendCtxKey, b)
}

func selectedBackendFromContext(ctx context.Context) (*backend.Backend, bool) {
	b, ok := ctx.Value(backendCtxKey).(*backend.Backend)
	return b, ok
}

func withRoute(ctx context.Context, r *router.Route) context.Context {
	return context.WithValue(ctx, routeCtxKey, r)
}

func routeFromContext(ctx context.Context) (*router.Route, bool) {
	r, ok := ctx.Value(routeCtxKey).(*router.Route)
	return r, ok
}

func withRetryAttempts(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, retryAttemptsCtxKey, n)
}

func retryAttemptsFromContext(ctx context.Context) (int, bool) {
	n, ok := ctx.Value(retryAttemptsCtxKey).(int)
	return n, ok
}
