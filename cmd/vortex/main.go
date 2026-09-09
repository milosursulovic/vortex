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
	"github.com/milosursulovic/vortex/internal/listener"
	"github.com/milosursulovic/vortex/internal/proxy"
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

	adminServer := admin.New(cfg.Admin.Address, log)
	serveErrCh := adminServer.Start()

	mgr, pools, err := startListeners(cfg, log)
	if err != nil {
		return fmt.Errorf("start listeners: %w", err)
	}

	healthCtx, stopHealthChecks := context.WithCancel(context.Background())
	defer stopHealthChecks()
	for _, pool := range pools {
		go health.NewMonitor(pool, cfg.HealthCheck, log).Run(healthCtx)
	}

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

// startListeners binds every configured listener: TCP listeners proxy to a
// shared flat backend pool, HTTP listeners route by host/path to named
// backend pools. It returns every backend pool built, so callers can health
// check them.
func startListeners(cfg *config.Config, log *slog.Logger) (*listener.Manager, []*backend.Pool, error) {
	mgr := listener.NewManager(log)
	var pools []*backend.Pool

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
		pools = append(pools, tcpPool)

		bal, err := balancer.New(cfg.LoadBalancing.Algorithm)
		if err != nil {
			return nil, nil, err
		}
		log.Info("load_balancing_algorithm_selected", "component", "tcp", "algorithm", bal.Name())
		tcpPicker = &balancer.Picker{Balancer: bal, Pool: tcpPool}
	}

	var httpProxy *proxy.HTTPProxy
	if hasHTTPListener {
		routes := make([]router.Route, 0, len(cfg.Routes))
		for _, rt := range cfg.Routes {
			poolCfg := findBackendPool(cfg.BackendPools, rt.BackendPool)
			pool := backend.NewPool(poolCfg.Backends)
			pools = append(pools, pool)

			bal, err := balancer.New(cfg.LoadBalancing.Algorithm)
			if err != nil {
				return nil, nil, err
			}

			routes = append(routes, router.Route{
				Host:     rt.Host,
				Path:     rt.Path,
				Pool:     pool,
				Balancer: bal,
			})
		}
		log.Info("load_balancing_algorithm_selected", "component", "http", "algorithm", cfg.LoadBalancing.Algorithm, "routes", len(routes))
		httpProxy = proxy.NewHTTPProxy(router.New(routes), cfg.Timeouts, log)
	}

	for _, l := range cfg.Listeners {
		switch l.Protocol {
		case "tcp":
			if err := mgr.StartTCP(l, tcpPicker, cfg.Timeouts); err != nil {
				return nil, nil, err
			}
		case "http":
			var tlsConfig *tls.Config
			if l.TLS != nil && l.TLS.Enabled {
				var err error
				tlsConfig, err = vortextls.LoadConfig(*l.TLS)
				if err != nil {
					return nil, nil, fmt.Errorf("listener %q: %w", l.Name, err)
				}
			}
			if err := mgr.StartHTTP(l, httpProxy, cfg.Timeouts, tlsConfig); err != nil {
				return nil, nil, err
			}
		}
	}

	return mgr, pools, nil
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
