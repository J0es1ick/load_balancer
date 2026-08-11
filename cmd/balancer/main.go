package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("balancer stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.InitConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	rootContext, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	if os.Getenv("MIGRATE_ONLY") == "true" {
		if cfg.RateLimit.Storage != "postgres" {
			return fmt.Errorf("MIGRATE_ONLY requires rate_limit.storage=postgres")
		}
		migrationContext, cancel := context.WithTimeout(rootContext, max(30*time.Second, cfg.Database.ConnectTimeout+30*time.Second))
		defer cancel()
		store, err := newRateLimitStore(migrationContext, cfg)
		if err != nil {
			return fmt.Errorf("apply PostgreSQL migrations: %w", err)
		}
		if err := store.Close(); err != nil {
			return fmt.Errorf("close PostgreSQL after migrations: %w", err)
		}
		slog.Info("PostgreSQL migrations applied")
		return nil
	}

	pool, err := balancer.NewBackendPool(backendSpecs(cfg.Backends), passivePolicy(cfg.HealthCheck, cfg.Server.Upstream))
	if err != nil {
		return fmt.Errorf("create backend pool: %w", err)
	}
	metrics := observability.NewMetrics()
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{
		Transport: upstreamTransport(cfg.Server.Upstream), Retry: retryPolicy(cfg.Server.Retry), Observer: metrics,
	})

	store, err := newRateLimitStore(rootContext, cfg)
	if err != nil {
		return fmt.Errorf("initialize rate limit storage: %w", err)
	}
	limiter, err := ratelimit.NewTokenBucketLimiter(limiterSettings(cfg.RateLimit), store, ratelimit.NewBoundedLocalStore(cfg.RateLimit.LocalShards, cfg.RateLimit.LocalMaxBuckets))
	if err != nil {
		_ = store.Close()
		return err
	}
	defer func() {
		if err := limiter.Close(); err != nil {
			slog.Error("close rate limit storage", "error", err)
		}
	}()
	limiter.StartCleanupWorker(rootContext, cfg.RateLimit.CleanupInterval, cfg.RateLimit.Retention)

	healthChecker, err := balancer.NewHealthChecker(pool, healthSettings(cfg.HealthCheck, cfg.Server.Upstream))
	if err != nil {
		return err
	}
	healthChecker.Check(rootContext)
	go healthChecker.Start(rootContext)

	var runtimeMu sync.Mutex
	currentConfig := cloneConfig(cfg)
	var httpServer *server.Server
	applyRuntime := func(ctx context.Context, update server.RuntimeUpdate) error {
		runtimeMu.Lock()
		defer runtimeMu.Unlock()
		next := cloneConfig(currentConfig)
		if err := mergeRuntimeUpdate(next, update); err != nil {
			return err
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := applyReload(ctx, currentConfig, next, pool, healthChecker, limiter, loadBalancer, httpServer); err != nil {
			return err
		}
		currentConfig = next
		return nil
	}

	managementToken, err := config.SecretFromEnv(cfg.Management.AuthTokenEnv)
	if err != nil && cfg.Management.Enabled && !cfg.Management.AllowInsecure {
		return err
	}
	httpServer, err = server.NewServer(serverOptions(cfg, managementToken, metrics, applyRuntime), loadBalancer, limiter)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}
	metrics.SetProviders(func() []observability.BackendMetric {
		snapshots := loadBalancer.Backends()
		result := make([]observability.BackendMetric, 0, len(snapshots))
		for _, backend := range snapshots {
			result = append(result, observability.BackendMetric{ID: backend.ID, Available: backend.Available})
		}
		return result
	}, func() observability.LimiterMetric {
		ctx, cancel := context.WithTimeout(context.Background(), limiter.Settings().OperationTimeout)
		err := limiter.Healthy(ctx)
		cancel()
		local := limiter.LocalStats()
		return observability.LimiterMetric{Storage: limiter.StorageName(), Healthy: err == nil, Degraded: err != nil && limiter.Settings().FailureMode != "fail-closed", LocalBuckets: local.Buckets, LocalEvictions: local.Evictions}
	})

	serverErrors := make(chan error, 1)
	go func() { serverErrors <- httpServer.Start() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)

	for {
		select {
		case signalValue := <-signals:
			if signalValue == syscall.SIGHUP {
				next, err := config.InitConfig()
				if err != nil {
					slog.Error("configuration reload rejected", "error", err)
					continue
				}
				runtimeMu.Lock()
				err = applyReload(rootContext, currentConfig, next, pool, healthChecker, limiter, loadBalancer, httpServer)
				if err == nil {
					currentConfig = cloneConfig(next)
				}
				runtimeMu.Unlock()
				if err != nil {
					slog.Error("configuration reload rejected", "error", err)
				} else {
					slog.Info("configuration reloaded")
				}
				continue
			}
			slog.Info("shutdown signal received", "signal", signalValue.String())
			return shutdown(httpServer, stopWorkers, currentConfig.Server.ShutdownTimeout)
		case err := <-serverErrors:
			if err != nil {
				return fmt.Errorf("HTTP server: %w", err)
			}
			return shutdown(httpServer, stopWorkers, currentConfig.Server.ShutdownTimeout)
		}
	}
}

func shutdown(httpServer *server.Server, stopWorkers context.CancelFunc, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	stopWorkers()
	slog.Info("server gracefully stopped")
	return nil
}
