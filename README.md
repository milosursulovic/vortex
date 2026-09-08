<p align="center">
  <img src="assets/logo.svg" width="120" alt="VORTEX logo">
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
