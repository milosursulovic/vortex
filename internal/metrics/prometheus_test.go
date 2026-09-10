package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/config"
)

func metricsHandlerForTest(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

func TestRegisterPoolCollectorExposesBackendMetrics(t *testing.T) {
	rec, reg := NewRecorder()
	rec.ConnectionsActive.Add(3)
	rec.RequestsTotal.Add(10)

	pool := backend.NewPool([]config.BackendConfig{{Name: "web-1", Address: "x:1", Weight: 1}})
	b, _ := pool.Get("web-1")
	b.IncActiveConnections()
	b.IncTotalConnections()

	pools := map[string]*backend.Pool{"frontend": pool}
	RegisterPoolCollector(reg, rec.Stats, pools)

	rec.ObserveRequestDuration(0)
	rec.ObserveBackendLatency("web-1", 0)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	handler := metricsHandlerForTest(reg)
	handler.ServeHTTP(rr, req)

	body := rr.Body.String()

	for _, want := range []string{
		"vortex_connections_active 3",
		"vortex_requests_total 10",
		`vortex_backend_connections{backend="web-1"} 1`,
		`vortex_backend_requests_total{backend="web-1"} 1`,
		`vortex_backend_health{backend="web-1"} 1`,
		"vortex_request_duration_seconds",
		`vortex_backend_latency_seconds_count{backend="web-1"}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected metrics output to contain %q, got:\n%s", want, body)
		}
	}
}

func TestBackendHealthReflectsDownState(t *testing.T) {
	rec, reg := NewRecorder()
	pool := backend.NewPool([]config.BackendConfig{{Name: "b1", Address: "x:1", Weight: 1}})
	pool.Disable("b1")
	RegisterPoolCollector(reg, rec.Stats, map[string]*backend.Pool{"tcp": pool})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	metricsHandlerForTest(reg).ServeHTTP(rr, req)

	if !strings.Contains(rr.Body.String(), `vortex_backend_health{backend="b1"} 0`) {
		t.Fatalf("expected backend_health 0 for a disabled backend, got:\n%s", rr.Body.String())
	}
}
