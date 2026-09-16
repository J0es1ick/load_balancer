package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
)

func gatewayDefaults(cfg *config.Config) gateway.Defaults {
	return gateway.Defaults{Upstream: cfg.Server.Upstream, Retry: retryPolicy(cfg.Server.Retry), Health: healthSettings(cfg.HealthCheck, cfg.Server.Upstream), WarmupTimeout: 15 * time.Second, WrapTransport: observability.TraceTransport}
}

func runGateway(cfg *config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdownTrace, err := observability.ConfigureTracing(ctx, cfg.Telemetry)
	if err != nil {
		return err
	}
	defer func() {
		flush, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = shutdownTrace(flush)
	}()
	metrics := observability.NewMetrics()
	engine, err := gateway.New(ctx, cfg.Gateway, gatewayDefaults(cfg), metrics)
	if err != nil {
		return fmt.Errorf("initialize gateway: %w", err)
	}
	defer engine.Close()
	store, err := newRateLimitStore(ctx, cfg)
	if err != nil {
		return err
	}
	limiter, err := ratelimit.NewTokenBucketLimiter(limiterSettings(cfg.RateLimit), store, ratelimit.NewBoundedLocalStore(cfg.RateLimit.LocalShards, cfg.RateLimit.LocalMaxBuckets))
	if err != nil {
		store.Close()
		return err
	}
	defer limiter.Close()
	limiter.StartCleanupWorker(ctx, cfg.RateLimit.CleanupInterval, cfg.RateLimit.Retention)
	options := serverOptions(cfg, "", metrics, nil)
	options.Gateway = engine
	options.Credentials = server.CredentialsFromConfig(cfg.Management.Credentials)
	options.ManagementTLS = cfg.Management.TLS
	if cfg.Management.AuthTokenEnv != "" {
		name := cfg.Management.AuthTokenEnv
		if _, err := config.SecretFromEnv(name); err != nil && cfg.Management.Enabled && !cfg.Management.AllowInsecure {
			return err
		}
		options.Credentials = append(options.Credentials, server.Credential{Name: "admin", Role: "admin", Token: func() (string, error) { return config.SecretFromEnv(name) }})
	}
	for _, credential := range options.Credentials {
		if credential.Token != nil {
			if _, err := credential.Token(); err != nil {
				return fmt.Errorf("credential %s: %w", credential.Name, err)
			}
		}
	}
	options.MetricsAddress = cfg.Management.MetricsAddress
	if cfg.Management.MetricsAuthTokenEnv != "" {
		name := cfg.Management.MetricsAuthTokenEnv
		if _, err := config.SecretFromEnv(name); err != nil {
			return err
		}
		options.MetricsToken = func() (string, error) { return config.SecretFromEnv(name) }
	}
	httpServer, err := server.NewServer(options, nil, limiter)
	if err != nil {
		return err
	}
	metrics.SetProviders(func() []observability.BackendMetric {
		state := engine.Status()
		result := []observability.BackendMetric{}
		for _, cluster := range state.Clusters {
			for _, endpoint := range cluster.Endpoints {
				result = append(result, observability.BackendMetric{ID: endpoint.ID, Cluster: cluster.ID, Available: endpoint.Available})
			}
		}
		return result
	}, func() observability.LimiterMetric {
		check, done := context.WithTimeout(context.Background(), limiter.Settings().OperationTimeout)
		defer done()
		err := limiter.Healthy(check)
		local := limiter.LocalStats()
		return observability.LimiterMetric{Storage: limiter.StorageName(), Healthy: err == nil, Degraded: err != nil && limiter.Settings().FailureMode != "fail-closed", LocalBuckets: local.Buckets, LocalEvictions: local.Evictions}
	})
	metrics.SetGatewayProvider(func() observability.GatewayMetric {
		state := engine.Status()
		result := observability.GatewayMetric{Revision: state.Revision, Hash: state.Hash, AppliedAt: state.AppliedAt, ApplySuccess: state.Stats.ApplySuccess, ApplyFailures: state.Stats.ApplyFailures}
		for _, cluster := range state.Clusters {
			value := observability.ClusterMetric{ID: cluster.ID, DiscoveryStale: cluster.Discovery.Stale, LastUpdate: cluster.Discovery.LastUpdate}
			for _, endpoint := range cluster.Endpoints {
				value.Inflight += endpoint.Inflight
				if endpoint.Available {
					value.AvailableEndpoints++
				}
			}
			result.Clusters = append(result.Clusters, value)
		}
		return result
	})
	if os.Getenv("DISCOVERY_MODE") != "external" {
		go runLocalDiscovery(ctx, engine)
	}
	errors := make(chan error, 1)
	go func() { errors <- httpServer.Start() }()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	for {
		select {
		case value := <-signals:
			if value == syscall.SIGHUP {
				next, err := config.InitConfig()
				if err == nil {
					err = config.ValidateReload(cfg, next)
				}
				if err == nil && next.Gateway == nil {
					err = fmt.Errorf("gateway configuration cannot be removed during reload")
				}
				if err == nil {
					err = httpServer.ApplyGatewayConfig(ctx, next.Gateway)
				}
				if err == nil {
					err = limiter.Reconfigure(limiterSettings(next.RateLimit))
					if err == nil {
						err = httpServer.UpdateRuntime(next.Server.TrustedProxies, healthSettings(next.HealthCheck, next.Server.Upstream))
					}
					if err == nil {
						cfg = next
					}
				}
				if err != nil {
					slog.Error("configuration reload rejected", "error", err)
				} else {
					slog.Info("configuration reloaded", "hash", engine.Status().Hash)
				}
				continue
			}
			return shutdown(httpServer, cancel, cfg.Server.ShutdownTimeout)
		case err := <-errors:
			if err != nil {
				return err
			}
			return shutdown(httpServer, cancel, cfg.Server.ShutdownTimeout)
		}
	}
}
