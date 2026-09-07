// Command vortex runs the VORTEX load balancer.
package main

import (
	"context"
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

	tcpManager, pool, err := startTCPListeners(cfg, log)
	if err != nil {
		return fmt.Errorf("start tcp listeners: %w", err)
	}

	healthCtx, stopHealthChecks := context.WithCancel(context.Background())
	defer stopHealthChecks()
	go health.NewMonitor(pool, cfg.HealthCheck, log).Run(healthCtx)

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

	if err := tcpManager.Close(); err != nil {
		log.Error("tcp_listener_close_failed", "error", err.Error())
	}
	tcpManager.WaitClosed(shutdownCtx)

	log.Info("shutdown_complete")
	return nil
}

// startTCPListeners binds every TCP listener from the config, proxying to a
// shared backend pool through the configured load-balancing algorithm.
// HTTP listeners are accepted in config but not yet served (added in a
// later phase).
func startTCPListeners(cfg *config.Config, log *slog.Logger) (*listener.Manager, *backend.Pool, error) {
	pool := backend.NewPool(cfg.Backends)

	bal, err := balancer.New(cfg.LoadBalancing.Algorithm)
	if err != nil {
		return nil, nil, err
	}
	log.Info("load_balancing_algorithm_selected", "algorithm", bal.Name())

	picker := &balancer.Picker{Balancer: bal, Pool: pool}
	mgr := listener.NewManager(log)

	for _, l := range cfg.Listeners {
		switch l.Protocol {
		case "tcp":
			if err := mgr.StartTCP(l, picker, cfg.Timeouts); err != nil {
				return nil, nil, err
			}
		case "http":
			log.Warn("http_listener_not_yet_implemented", "name", l.Name, "address", l.Address)
		}
	}

	return mgr, pool, nil
}
