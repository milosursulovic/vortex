// Command vortex runs the VORTEX load balancer.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/milosursulovic/vortex/internal/admin"
	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/balancer"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/health"
	"github.com/milosursulovic/vortex/internal/limits"
	"github.com/milosursulovic/vortex/internal/listener"
	"github.com/milosursulovic/vortex/internal/metrics"
	"github.com/milosursulovic/vortex/internal/proxy"
	"github.com/milosursulovic/vortex/internal/ratelimit"
	"github.com/milosursulovic/vortex/internal/reload"
	"github.com/milosursulovic/vortex/internal/router"
	vortextls "github.com/milosursulovic/vortex/internal/tls"
	"github.com/milosursulovic/vortex/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vortex:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "configs/vortex.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New(cfg.Logging.Level, cfg.Logging.Format)
	stats := metrics.New()

	mgr, pools, rateLimiter, err := startListeners(cfg, stats, log)
	if err != nil {
		return fmt.Errorf("start listeners: %w", err)
	}

	registry := admin.NewRegistry(pools)

	var adminServer *admin.Server
	reloadFn := func() error {
		newCfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		reload.Apply(newCfg, pools, log)
		if adminServer != nil {
			adminServer.UpdateConfig(newCfg)
		}
		log.Info("configuration_reloaded")
		return nil
	}
	adminServer = admin.New(cfg.Admin.Address, log, registry, stats, cfg, reloadFn)
	serveErrCh := adminServer.Start()

	backgroundCtx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	for _, pool := range pools {
		go health.NewMonitor(pool, cfg.HealthCheck, log).Run(backgroundCtx)
	}
	if rateLimiter != nil {
		go rateLimiter.Run(backgroundCtx)
	}
	go watchReloadSignal(backgroundCtx, reloadFn, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		log.Info("shutdown_signal_received")
	case err := <-serveErrCh:
		if err != nil {
			return fmt.Errorf("admin server: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Duration())
	defer cancel()

	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("admin server shutdown: %w", err)
	}

	if err := mgr.Close(); err != nil {
		log.Error("listener_close_failed", "error", err.Error())
	}
	mgr.WaitClosed(shutdownCtx)

	log.Info("shutdown_complete")
	return nil
}

// watchReloadSignal re-applies config on SIGHUP until ctx is done, mirroring
// what POST /admin/reload does.
func watchReloadSignal(ctx context.Context, reloadFn func() error, log *slog.Logger) {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sighup:
			log.Info("reload_signal_received")
			if err := reloadFn(); err != nil {
				log.Error("reload_failed", "error", err.Error())
			}
		}
	}
}

// startListeners binds every configured listener: TCP listeners proxy to a
// shared flat backend pool, HTTP listeners route by host/path to named
// backend pools. Every pool gets a circuit breaker per cfg.CircuitBreaker,
// and a shared connection limiter (cfg.Limits.MaxConnections) gates both
// TCP and HTTP listeners. It returns every backend pool built, keyed "tcp"
// for the flat pool or by backend_pool name, so callers can health check
// and administer them; and the HTTP rate limiter (nil if disabled) so
// callers can run its background reaper.
func startListeners(cfg *config.Config, stats *metrics.Stats, log *slog.Logger) (*listener.Manager, map[string]*backend.Pool, *ratelimit.Limiter, error) {
	connLimiter := limits.NewConnLimiter(cfg.Limits.MaxConnections)
	mgr := listener.NewManager(log, connLimiter, stats)
	pools := make(map[string]*backend.Pool)

	hasTCPListener := false
	hasHTTPListener := false
	for _, l := range cfg.Listeners {
		switch l.Protocol {
		case "tcp":
			hasTCPListener = true
		case "http":
			hasHTTPListener = true
		}
	}

	var tcpPicker *balancer.Picker
	if hasTCPListener {
		tcpPool := backend.NewPool(cfg.Backends)
		tcpPool.ConfigureCircuitBreakers(cfg.CircuitBreaker)
		pools["tcp"] = tcpPool

		bal, err := balancer.New(cfg.LoadBalancing.Algorithm)
		if err != nil {
			return nil, nil, nil, err
		}
		log.Info("load_balancing_algorithm_selected", "component", "tcp", "algorithm", bal.Name())
		tcpPicker = &balancer.Picker{Balancer: bal, Pool: tcpPool}
	}

	var httpProxy *proxy.HTTPProxy
	var rateLimiter *ratelimit.Limiter
	if hasHTTPListener {
		routes := make([]router.Route, 0, len(cfg.Routes))
		for _, rt := range cfg.Routes {
			poolCfg := findBackendPool(cfg.BackendPools, rt.BackendPool)
			pool := backend.NewPool(poolCfg.Backends)
			pool.ConfigureCircuitBreakers(cfg.CircuitBreaker)
			pools[poolCfg.Name] = pool

			bal, err := balancer.New(cfg.LoadBalancing.Algorithm)
			if err != nil {
				return nil, nil, nil, err
			}

			routes = append(routes, router.Route{
				Host:     rt.Host,
				Path:     rt.Path,
				Pool:     pool,
				Balancer: bal,
			})
		}
		log.Info("load_balancing_algorithm_selected", "component", "http", "algorithm", cfg.LoadBalancing.Algorithm, "routes", len(routes))

		if cfg.RateLimit.Enabled {
			rateLimiter = ratelimit.New(cfg.RateLimit)
			log.Info("rate_limiting_enabled", "requests_per_second", cfg.RateLimit.RequestsPerSecond, "burst", cfg.RateLimit.Burst, "per_ip", cfg.RateLimit.PerIP)
		}

		httpProxy = proxy.NewHTTPProxy(router.New(routes), cfg.Timeouts, cfg.Limits, rateLimiter, cfg.Retry, stats, log)
	}

	for _, l := range cfg.Listeners {
		switch l.Protocol {
		case "tcp":
			if err := mgr.StartTCP(l, tcpPicker, cfg.Timeouts, cfg.Limits); err != nil {
				return nil, nil, nil, err
			}
		case "http":
			var tlsConfig *tls.Config
			if l.TLS != nil && l.TLS.Enabled {
				var err error
				tlsConfig, err = vortextls.LoadConfig(*l.TLS)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("listener %q: %w", l.Name, err)
				}
			}
			if err := mgr.StartHTTP(l, httpProxy, cfg.Timeouts, tlsConfig, cfg.Limits); err != nil {
				return nil, nil, nil, err
			}
		}
	}

	return mgr, pools, rateLimiter, nil
}

func findBackendPool(pools []config.BackendPoolConfig, name string) config.BackendPoolConfig {
	for _, p := range pools {
		if p.Name == name {
			return p
		}
	}
	// Unreachable: config.validate() already rejects routes referencing an
	// unknown backend_pool before this is ever called.
	return config.BackendPoolConfig{}
}
