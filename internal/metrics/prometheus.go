package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/milosursulovic/vortex/internal/backend"
)

// Recorder pairs the plain atomic Stats (already used for /admin/stats)
// with the two Prometheus histograms that need per-event Observe calls
// rather than a point-in-time read. It's built before backend pools exist,
// so the data-plane packages can start recording immediately; the
// pool-dependent gauges/counters are registered separately once pools are
// known (see RegisterPoolCollector).
type Recorder struct {
	*Stats

	requestDuration prometheus.Histogram
	backendLatency  *prometheus.HistogramVec
}

// NewRecorder builds a Recorder and the Prometheus registry it (and later
// RegisterPoolCollector) feed.
func NewRecorder() (*Recorder, *prometheus.Registry) {
	reg := prometheus.NewRegistry()

	requestDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "vortex_request_duration_seconds",
		Help:    "HTTP request duration in seconds, end to end through VORTEX.",
		Buckets: prometheus.DefBuckets,
	})
	backendLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vortex_backend_latency_seconds",
		Help:    "Time to connect to (TCP) or round-trip with (HTTP) a backend, in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"backend"})
	reg.MustRegister(requestDuration, backendLatency)

	return &Recorder{
		Stats:           New(),
		requestDuration: requestDuration,
		backendLatency:  backendLatency,
	}, reg
}

// ObserveRequestDuration records one HTTP request's total duration.
func (r *Recorder) ObserveRequestDuration(d time.Duration) {
	r.requestDuration.Observe(d.Seconds())
}

// ObserveBackendLatency records how long it took to connect to (TCP) or
// complete a round trip with (HTTP) a specific backend. Backend names are
// globally unique (config validation enforces this), so no pool label is
// needed to disambiguate.
func (r *Recorder) ObserveBackendLatency(backendName string, d time.Duration) {
	r.backendLatency.WithLabelValues(backendName).Observe(d.Seconds())
}

// RegisterPoolCollector adds the pools' live gauges/counters (connections,
// health, ...) to reg. Called once pools exist, after NewRecorder.
func RegisterPoolCollector(reg *prometheus.Registry, stats *Stats, pools map[string]*backend.Pool) {
	reg.MustRegister(newCollector(stats, pools))
}

// collector computes gauge/counter values fresh on every scrape, rather
// than keeping a second set of Prometheus-native counters in sync with the
// atomics in Stats and backend.Backend at every increment site.
type collector struct {
	stats *Stats
	pools map[string]*backend.Pool

	connectionsActive *prometheus.Desc
	connectionsTotal  *prometheus.Desc
	requestsTotal     *prometheus.Desc
	responsesTotal    *prometheus.Desc
	errorsTotal       *prometheus.Desc
	bytesReceived     *prometheus.Desc
	bytesSent         *prometheus.Desc

	backendConnections   *prometheus.Desc
	backendRequestsTotal *prometheus.Desc
	backendErrorsTotal   *prometheus.Desc
	backendHealth        *prometheus.Desc
}

func newCollector(stats *Stats, pools map[string]*backend.Pool) *collector {
	backendLabels := []string{"backend"}
	return &collector{
		stats: stats,
		pools: pools,

		connectionsActive: prometheus.NewDesc("vortex_connections_active", "Current active connections (TCP + HTTP).", nil, nil),
		connectionsTotal:  prometheus.NewDesc("vortex_connections_total", "Total connections accepted.", nil, nil),
		requestsTotal:     prometheus.NewDesc("vortex_requests_total", "Total HTTP requests received.", nil, nil),
		responsesTotal:    prometheus.NewDesc("vortex_responses_total", "Total HTTP responses successfully returned by a backend.", nil, nil),
		errorsTotal:       prometheus.NewDesc("vortex_errors_total", "Total connections/requests VORTEX failed to serve.", nil, nil),
		bytesReceived:     prometheus.NewDesc("vortex_bytes_received_total", "Total bytes received from clients.", nil, nil),
		bytesSent:         prometheus.NewDesc("vortex_bytes_sent_total", "Total bytes sent to clients.", nil, nil),

		backendConnections:   prometheus.NewDesc("vortex_backend_connections", "Current active connections to a backend.", backendLabels, nil),
		backendRequestsTotal: prometheus.NewDesc("vortex_backend_requests_total", "Total connections/requests dispatched to a backend.", backendLabels, nil),
		backendErrorsTotal:   prometheus.NewDesc("vortex_backend_errors_total", "Total failures dispatching to a backend.", backendLabels, nil),
		backendHealth:        prometheus.NewDesc("vortex_backend_health", "1 if the backend is UP, 0 otherwise.", backendLabels, nil),
	}
}

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connectionsActive
	ch <- c.connectionsTotal
	ch <- c.requestsTotal
	ch <- c.responsesTotal
	ch <- c.errorsTotal
	ch <- c.bytesReceived
	ch <- c.bytesSent
	ch <- c.backendConnections
	ch <- c.backendRequestsTotal
	ch <- c.backendErrorsTotal
	ch <- c.backendHealth
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	snap := c.stats.Snapshot()
	ch <- prometheus.MustNewConstMetric(c.connectionsActive, prometheus.GaugeValue, float64(snap.Connections.Active))
	ch <- prometheus.MustNewConstMetric(c.connectionsTotal, prometheus.CounterValue, float64(snap.Connections.Total))
	ch <- prometheus.MustNewConstMetric(c.requestsTotal, prometheus.CounterValue, float64(snap.Requests.Total))
	ch <- prometheus.MustNewConstMetric(c.responsesTotal, prometheus.CounterValue, float64(snap.Requests.Responses))
	ch <- prometheus.MustNewConstMetric(c.errorsTotal, prometheus.CounterValue, float64(snap.Requests.Errors))
	ch <- prometheus.MustNewConstMetric(c.bytesReceived, prometheus.CounterValue, float64(snap.Bytes.Received))
	ch <- prometheus.MustNewConstMetric(c.bytesSent, prometheus.CounterValue, float64(snap.Bytes.Sent))

	for _, pool := range c.pools {
		for _, b := range pool.All() {
			ch <- prometheus.MustNewConstMetric(c.backendConnections, prometheus.GaugeValue, float64(b.ActiveConnections()), b.Name)
			ch <- prometheus.MustNewConstMetric(c.backendRequestsTotal, prometheus.CounterValue, float64(b.TotalConnections()), b.Name)
			ch <- prometheus.MustNewConstMetric(c.backendErrorsTotal, prometheus.CounterValue, float64(b.Failures()), b.Name)
			health := 0.0
			if b.State() == backend.StateUp {
				health = 1
			}
			ch <- prometheus.MustNewConstMetric(c.backendHealth, prometheus.GaugeValue, health, b.Name)
		}
	}
}
