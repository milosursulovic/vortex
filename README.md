<p align="center">
  <img src="assets/logo.png" width="120" alt="VORTEX logo">
</p>

<h1 align="center">VORTEX</h1>

High-performance Layer 4 (TCP) / Layer 7 (HTTP) load balancer built from scratch in Go.

Full project specification: [docs/vortex.pdf](docs/vortex.pdf).

## Build & run

```sh
make build
make run
```

Config file defaults to `configs/vortex.yaml` (override with `-config`).
Admin/health server listens on `admin.address` (default `:9090`); check `GET /health`.

## Admin API & CLI

The admin server (private by default — never expose it publicly) also serves:

```
GET  /admin/backends              list every backend across all pools
GET  /admin/backends/{name}       show one backend
POST /admin/backends/{name}/enable
POST /admin/backends/{name}/disable
POST /admin/backends/{name}/drain
GET  /admin/stats                 runtime counters (connections, requests, bytes)
GET  /admin/config                the currently running config
POST /admin/reload                re-read the config file (also triggered by SIGHUP)
```

`vortexctl` is a small CLI over that API:

```sh
go build -o bin/vortexctl ./cmd/vortexctl
vortexctl -admin-addr http://localhost:9090 status
vortexctl -admin-addr http://localhost:9090 backend list
vortexctl -admin-addr http://localhost:9090 backend drain web-1
vortexctl -admin-addr http://localhost:9090 reload
```

Reload re-reads the config file and reconciles backend pool membership and
per-backend address/weight (add/update/remove), preserving already-open
connections. It does not rebind listeners, change TLS certificates, or
swap the load-balancing algorithm — those need a restart.

## Observability

- **Metrics**: Prometheus format at `GET /metrics` on the admin server —
  connection/request/byte counters, per-backend connections/errors/health,
  and request/backend-latency histograms. Always on, no config needed.
- **Tracing**: optional OpenTelemetry tracing for HTTP requests (`tracing.*`
  in the config) — a span per request plus a child span per backend round
  trip, with routing decision, selected backend, and errors as attributes.
  Trace context propagates to the backend via a standard `traceparent`
  header. Exports to stdout or an OTLP/HTTP collector; disabled by default.
- **Profiling**: `debug.pprof_enabled: true` mounts `net/http/pprof` under
  `/debug/pprof/` on the admin server (already private) for CPU, heap,
  goroutine, mutex, and blocking profiles. Off by default — sampling has
  real overhead.
- **Logs**: structured JSON via `log/slog` to stdout (from Phase 1).

## Development

```sh
make test   # unit tests
make race   # race detector
make vet    # go vet
```

## Status

In active development, following the phased plan in the spec (Go Foundation →
TCP Proxy → Backend Pool → Load Balancing → Health Checking → HTTP Proxy →
TLS → Resilience → Runtime Management → Observability → Performance → Linux
Optimization).
