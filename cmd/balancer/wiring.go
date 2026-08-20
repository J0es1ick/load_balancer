package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
)

func applyReload(ctx context.Context, current, next *config.Config, pool *balancer.BackendPool, healthChecker *balancer.HealthChecker, limiter *ratelimit.TokenBucketLimiter, loadBalancer *balancer.LoadBalancer, httpServer *server.Server) error {
	if err := config.ValidateReload(current, next); err != nil {
		return err
	}
	replacement, err := pool.PrepareReplacement(backendSpecs(next.Backends))
	if err != nil {
		return err
	}
	nextHealth := healthSettings(next.HealthCheck, next.Server.Upstream)
	if err := healthChecker.WarmReplacement(ctx, replacement, nextHealth); err != nil {
		return fmt.Errorf("warm backend replacement: %w", err)
	}
	if err := limiter.Reconfigure(limiterSettings(next.RateLimit)); err != nil {
		return fmt.Errorf("reconfigure rate limiter: %w", err)
	}
	if err := healthChecker.Update(nextHealth); err != nil {
		return err
	}
	loadBalancer.UpdateRetryPolicy(retryPolicy(next.Server.Retry))
	if httpServer != nil {
		if err := httpServer.UpdateRuntime(next.Server.TrustedProxies, nextHealth); err != nil {
			return err
		}
	}
	if err := pool.CommitReplacement(replacement); err != nil {
		return fmt.Errorf("commit backend replacement: %w", err)
	}
	return nil
}

func newRateLimitStore(ctx context.Context, cfg *config.Config) (ratelimit.Store, error) {
	switch cfg.RateLimit.Storage {
	case "local":
		return ratelimit.NewBoundedLocalStore(cfg.RateLimit.LocalShards, cfg.RateLimit.LocalMaxBuckets), nil
	case "redis":
		password, err := config.SecretFromEnv(cfg.Redis.PasswordEnv)
		if err != nil {
			return nil, err
		}
		return ratelimit.NewRedisStore(ctx, ratelimit.RedisOptions{Address: cfg.Redis.Address, Password: password, Database: cfg.Redis.Database, PoolSize: cfg.Redis.PoolSize, DialTimeout: cfg.Redis.DialTimeout, ReadTimeout: cfg.Redis.ReadTimeout, WriteTimeout: cfg.Redis.WriteTimeout, Retention: cfg.RateLimit.Retention})
	case "postgres":
		password, err := config.SecretFromEnv(cfg.Database.PasswordEnv)
		if err != nil {
			return nil, err
		}
		return ratelimit.NewPostgresStore(ctx, ratelimit.PostgresOptions{Host: cfg.Database.Host, Port: cfg.Database.Port, User: cfg.Database.User, Password: password, Database: cfg.Database.Name, SSLMode: cfg.Database.SSLMode, ConnectTimeout: cfg.Database.ConnectTimeout, MaxOpenConns: cfg.Database.MaxOpenConns})
	default:
		return nil, fmt.Errorf("unknown rate limit storage %q", cfg.RateLimit.Storage)
	}
}

func upstreamTransport(cfg config.UpstreamConfig) *http.Transport {
	return &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: cfg.MaxIdleConns, MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost, MaxConnsPerHost: cfg.MaxConnsPerHost, IdleConnTimeout: cfg.IdleConnTimeout, TLSHandshakeTimeout: cfg.TLSHandshakeTimeout, ExpectContinueTimeout: cfg.ExpectContinueTimeout, ResponseHeaderTimeout: cfg.ResponseHeaderTimeout, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
}

func backendSpecs(backends []config.BackendConfig) []balancer.BackendSpec {
	result := make([]balancer.BackendSpec, 0, len(backends))
	for _, backend := range backends {
		result = append(result, balancer.BackendSpec{ID: backend.ID, URL: backend.URL, Disabled: backend.Disabled})
	}
	return result
}

func passivePolicy(cfg config.HealthCheckConfig, upstream ...config.UpstreamConfig) balancer.PassivePolicy {
	return balancer.PassivePolicy{FailureThreshold: int64(cfg.FailureThreshold), Cooldown: cfg.Cooldown, MaxConcurrentRequests: backendConcurrency(upstream), SlowStart: cfg.SlowStart, SlowStartMinimum: cfg.SlowStartMinimum}
}

func healthSettings(cfg config.HealthCheckConfig, upstream ...config.UpstreamConfig) balancer.HealthSettings {
	return balancer.HealthSettings{Mode: cfg.Mode, Path: cfg.Path, Interval: cfg.Interval, Timeout: cfg.Timeout, FailureThreshold: cfg.FailureThreshold, SuccessThreshold: cfg.SuccessThreshold, MaxConcurrency: cfg.MaxConcurrency, Jitter: cfg.Jitter, Cooldown: cfg.Cooldown, ExpectedStatuses: append([]int(nil), cfg.ExpectedStatuses...), SlowStart: cfg.SlowStart, SlowStartMinimum: cfg.SlowStartMinimum, MaxConcurrentRequests: backendConcurrency(upstream)}
}

func backendConcurrency(upstream []config.UpstreamConfig) int64 {
	if len(upstream) == 0 {
		return 512
	}
	return int64(upstream[0].MaxConcurrentRequests)
}

func limiterSettings(cfg config.RateLimitConfig) ratelimit.RuntimeSettings {
	return ratelimit.RuntimeSettings{Enabled: cfg.Enabled, Policy: ratelimit.Policy{Capacity: cfg.Capacity, RefillPerSecond: cfg.RefillPerSecond}, FailureMode: cfg.FailureMode, OperationTimeout: cfg.OperationTimeout, IPv4PrefixBits: cfg.IPv4PrefixBits, IPv6PrefixBits: cfg.IPv6PrefixBits}
}

func retryPolicy(cfg config.RetryConfig) balancer.RetryPolicy {
	return balancer.RetryPolicy{MaxAttempts: cfg.MaxAttempts, PerTryTimeout: cfg.PerTryTimeout, BodyLimit: cfg.BodyLimit, Methods: append([]string(nil), cfg.Methods...), Statuses: append([]int(nil), cfg.Statuses...), BudgetCapacity: cfg.BudgetCapacity, BudgetRefillPerSecond: cfg.BudgetRefillPerSecond}
}

func serverOptions(cfg *config.Config, managementToken string, metrics *observability.Metrics, apply func(context.Context, server.RuntimeUpdate) error) server.Options {
	instanceID, err := os.Hostname()
	if err != nil || instanceID == "" {
		instanceID = "unknown"
	}
	return server.Options{Port: cfg.Server.Port, ManagementEnabled: cfg.Management.Enabled, ManagementAddress: cfg.Management.Address, ManagementAuthToken: managementToken, ManagementInsecure: cfg.Management.AllowInsecure, RuntimeMutations: cfg.Management.RuntimeMutations, InstanceID: instanceID, EnablePprof: cfg.Management.EnablePprof, TrustedProxies: append([]string(nil), cfg.Server.TrustedProxies...), AccessLogSampleRate: cfg.Server.AccessLogSampleRate, AccessLogIncludePath: cfg.Server.AccessLogIncludePath, ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout, ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout, ManagementWriteTimeout: cfg.Management.WriteTimeout, IdleTimeout: cfg.Server.IdleTimeout, MaxHeaderBytes: cfg.Server.MaxHeaderBytes, OverloadMaxConcurrent: cfg.Server.Overload.MaxConcurrentRequests, OverloadQueueTimeout: cfg.Server.Overload.QueueTimeout, Health: healthSettings(cfg.HealthCheck, cfg.Server.Upstream), Metrics: metrics, ApplyRuntime: apply}
}
