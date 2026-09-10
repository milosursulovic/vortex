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

## Linux networking

Go's runtime already uses epoll internally for all network I/O, and
`*net.TCPConn` already defaults to `TCP_NODELAY` on — that's the baseline,
not a problem to route around. `network.*` in the config adds a small set
of additional opt-in knobs (all default to Go's own behavior):

- `reuse_port_workers: N` — binds N sockets to the same address
  (`SO_REUSEPORT`) instead of one, so the kernel spreads accepted
  connections across N accept-loop goroutines. Can be overridden per
  listener. Only worth setting if profiling shows accept-loop contention
  under your actual load — it wasn't needed to reach the numbers below.
- `keepalive_enabled` / `keepalive_interval`, `tcp_nodelay`,
  `read_buffer_bytes` / `write_buffer_bytes` — direct overrides of Go's
  socket defaults, for an operator who has a specific reason to change
  them.

Heavier techniques the spec names as an optional, separate track
(io_uring, eBPF, a custom event loop, zero-copy `splice`/`sendfile`) are
deliberately not implemented here: nothing in the Phase 11 profiling showed
VORTEX's own code as the bottleneck (CPU time was syscalls and GC, the
expected shape for a proxy), so the spec's own gate — "only after
profiling demonstrates a real bottleneck" — isn't met. They belong in an
experimental branch if a specific workload ever needs them, not folded
into the main implementation speculatively.

## Development

```sh
make test   # unit tests
make race   # race detector
make vet    # go vet
make bench  # benchmarks (load-balancing algorithms, buffer pooling)
```

`test/integration` drives the real proxy (not mocks) under concurrent load
and checks for leaked connections and negative counters — run it with
`-race`. Two buffer pools (the TCP copy buffer and the HTTP reverse proxy's
copy buffer) and a cached consistent-hashing ring exist because profiling
under load showed them as the top allocators/hot path, not by default —
see the comments at their definitions for the benchmarks that justified
each one.

## Status

All 12 phases of the spec's build plan are implemented: Go Foundation →
TCP Proxy → Backend Pool → Load Balancing → Health Checking → HTTP Proxy →
TLS → Resilience → Runtime Management → Observability → Performance →
Linux Optimization.
