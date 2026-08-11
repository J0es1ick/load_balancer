package main

import (
	"context"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyReloadUpdatesDynamicComponentsWithoutStorageRewrite(t *testing.T) {
	current := reloadTestConfig()
	next := cloneConfig(current)
	next.Backends = []config.BackendConfig{{ID: "replacement", URL: "http://127.0.0.1:2"}}
	next.RateLimit.Capacity = 4
	next.RateLimit.RefillPerSecond = 2.5
	next.HealthCheck.Interval = 10 * time.Second
	next.HealthCheck.Timeout = 10 * time.Millisecond
	next.Server.TrustedProxies = []string{"192.0.2.0/24"}
	next.Server.Retry.MaxAttempts = 3

	pool, err := balancer.NewBackendPool(backendSpecs(current.Backends))
	require.NoError(t, err)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	limiter, err := ratelimit.NewTokenBucketLimiter(limiterSettings(current.RateLimit), ratelimit.NewLocalStore(4), nil)
	require.NoError(t, err)
	healthChecker, err := balancer.NewHealthChecker(pool, healthSettings(current.HealthCheck))
	require.NoError(t, err)
	httpServer, err := server.NewServer(serverOptions(current, "", observability.NewMetrics(), nil), loadBalancer, limiter)
	require.NoError(t, err)

	require.NoError(t, applyReload(context.Background(), current, next, pool, healthChecker, limiter, loadBalancer, httpServer))
	require.Len(t, pool.GetBackends(), 1)
	assert.Equal(t, "replacement", pool.GetBackends()[0].ID())
	assert.Equal(t, 4, limiter.Settings().Policy.Capacity)
	assert.Equal(t, 2.5, limiter.Settings().Policy.RefillPerSecond)
	assert.Equal(t, 10*time.Second, healthChecker.Settings().Interval)
}

func reloadTestConfig() *config.Config {
	return &config.Config{
		Server:      config.ServerConfig{Port: "8080", ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, MaxHeaderBytes: 1 << 20, Upstream: config.UpstreamConfig{DialTimeout: time.Second, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: time.Second, ExpectContinueTimeout: time.Second, IdleConnTimeout: time.Second, MaxIdleConns: 10, MaxIdleConnsPerHost: 2, MaxConcurrentRequests: 32}, Retry: config.RetryConfig{MaxAttempts: 1, PerTryTimeout: time.Second, BodyLimit: 1024, Methods: []string{"GET"}, Statuses: []int{503}, BudgetCapacity: 10, BudgetRefillPerSecond: 1}, Overload: config.OverloadConfig{MaxConcurrentRequests: 64, QueueTimeout: time.Millisecond}},
		Management:  config.ManagementConfig{Enabled: false},
		Database:    config.DatabaseConfig{ConnectTimeout: time.Second, MaxOpenConns: 1},
		Redis:       config.RedisConfig{Address: "redis:6379", PoolSize: 1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second},
		Backends:    []config.BackendConfig{{ID: "original", URL: "http://127.0.0.1:1"}},
		RateLimit:   config.RateLimitConfig{Enabled: true, Storage: "local", FailureMode: "fail-open", Capacity: 10, RefillPerSecond: 1, OperationTimeout: time.Second, LocalShards: 4, CleanupInterval: time.Hour, Retention: time.Hour},
		HealthCheck: config.HealthCheckConfig{Mode: "tcp", Path: "/health", Interval: 5 * time.Second, Timeout: 10 * time.Millisecond, FailureThreshold: 2, SuccessThreshold: 1, MaxConcurrency: 2, ExpectedStatuses: []int{200}, SlowStartMinimum: 10},
	}
}
