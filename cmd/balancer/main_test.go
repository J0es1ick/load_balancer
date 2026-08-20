package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()
	current := reloadTestConfig()
	next := cloneConfig(current)
	next.Backends = []config.BackendConfig{{ID: "replacement", URL: backendServer.URL}}
	next.RateLimit.Capacity = 4
	next.RateLimit.RefillPerSecond = 2.5
	next.HealthCheck.Interval = 10 * time.Second
	next.HealthCheck.Timeout = time.Second
	next.HealthCheck.Mode = "http"
	next.Server.TrustedProxies = []string{"192.0.2.0/24"}
	next.Server.Retry.MaxAttempts = 3

	pool, err := balancer.NewBackendPool(backendSpecs(current.Backends))
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
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
	assert.True(t, pool.Ready())
}

func TestApplyReloadKeepsCurrentPoolUntilReplacementIsWarm(t *testing.T) {
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(probeStarted)
		<-releaseProbe
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()

	current := reloadTestConfig()
	next := cloneConfig(current)
	next.Backends = []config.BackendConfig{{ID: "replacement", URL: backendServer.URL}}
	next.HealthCheck.Mode = "http"
	next.HealthCheck.Timeout = time.Second

	pool, err := balancer.NewBackendPool(backendSpecs(current.Backends))
	require.NoError(t, err)
	original := pool.GetBackends()[0]
	original.SetAlive(true)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	limiter, err := ratelimit.NewTokenBucketLimiter(limiterSettings(current.RateLimit), ratelimit.NewLocalStore(4), nil)
	require.NoError(t, err)
	healthChecker, err := balancer.NewHealthChecker(pool, healthSettings(current.HealthCheck))
	require.NoError(t, err)

	reloadDone := make(chan error, 1)
	go func() {
		reloadDone <- applyReload(context.Background(), current, next, pool, healthChecker, limiter, loadBalancer, nil)
	}()
	<-probeStarted
	assert.Same(t, original, pool.GetBackends()[0])
	assert.True(t, pool.Ready(), "the current pool must remain routable during replacement warm-up")
	close(releaseProbe)
	require.NoError(t, <-reloadDone)
	assert.Equal(t, "replacement", pool.GetBackends()[0].ID())
	assert.True(t, pool.Ready())
}

func TestApplyReloadRejectsUnavailableReplacementWithoutPublishingIt(t *testing.T) {
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unavailableURL := unavailable.URL
	unavailable.Close()

	current := reloadTestConfig()
	next := cloneConfig(current)
	next.Backends = []config.BackendConfig{{ID: "unavailable", URL: unavailableURL}}
	next.HealthCheck.Mode = "http"
	next.HealthCheck.Timeout = 100 * time.Millisecond

	pool, err := balancer.NewBackendPool(backendSpecs(current.Backends))
	require.NoError(t, err)
	original := pool.GetBackends()[0]
	original.SetAlive(true)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	limiter, err := ratelimit.NewTokenBucketLimiter(limiterSettings(current.RateLimit), ratelimit.NewLocalStore(4), nil)
	require.NoError(t, err)
	healthChecker, err := balancer.NewHealthChecker(pool, healthSettings(current.HealthCheck))
	require.NoError(t, err)

	err = applyReload(context.Background(), current, next, pool, healthChecker, limiter, loadBalancer, nil)
	assert.ErrorContains(t, err, "backend replacement is not ready")
	assert.Same(t, original, pool.GetBackends()[0])
	assert.True(t, pool.Ready())
}

func TestApplyReloadServesParallelTrafficWhileReplacementWarms(t *testing.T) {
	originalServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("original"))
	}))
	defer originalServer.Close()

	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	var probeOnce sync.Once
	replacementServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			probeOnce.Do(func() { close(probeStarted) })
			<-releaseProbe
		}
		_, _ = writer.Write([]byte("replacement"))
	}))
	defer replacementServer.Close()

	current := reloadTestConfig()
	current.Backends = []config.BackendConfig{{ID: "original", URL: originalServer.URL}}
	next := cloneConfig(current)
	next.Backends = []config.BackendConfig{{ID: "replacement", URL: replacementServer.URL}}
	next.HealthCheck.Mode = "http"
	next.HealthCheck.Path = "/health"
	next.HealthCheck.Timeout = time.Second

	pool, err := balancer.NewBackendPool(backendSpecs(current.Backends))
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	limiter, err := ratelimit.NewTokenBucketLimiter(limiterSettings(current.RateLimit), ratelimit.NewLocalStore(4), nil)
	require.NoError(t, err)
	healthChecker, err := balancer.NewHealthChecker(pool, healthSettings(current.HealthCheck))
	require.NoError(t, err)

	reloadDone := make(chan error, 1)
	go func() {
		reloadDone <- applyReload(context.Background(), current, next, pool, healthChecker, limiter, loadBalancer, nil)
	}()
	<-probeStarted

	trafficContext, stopTraffic := context.WithCancel(context.Background())
	var traffic sync.WaitGroup
	var completed atomic.Int64
	originalSeen := make(chan struct{})
	replacementSeen := make(chan struct{})
	var originalOnce sync.Once
	var replacementOnce sync.Once
	errors := make(chan string, 16)
	for range 8 {
		traffic.Add(1)
		go func() {
			defer traffic.Done()
			for trafficContext.Err() == nil {
				recorder := httptest.NewRecorder()
				loadBalancer.ServeHTTP(recorder, httptest.NewRequestWithContext(trafficContext, http.MethodGet, "http://balancer.test/resource", nil))
				if trafficContext.Err() != nil {
					return
				}
				if recorder.Code != http.StatusOK {
					select {
					case errors <- recorder.Result().Status:
					default:
					}
					return
				}
				body := recorder.Body.String()
				switch body {
				case "original":
					originalOnce.Do(func() { close(originalSeen) })
				case "replacement":
					replacementOnce.Do(func() { close(replacementSeen) })
				default:
					select {
					case errors <- body:
					default:
					}
					return
				}
				completed.Add(1)
			}
		}()
	}
	defer func() {
		stopTraffic()
		traffic.Wait()
	}()

	select {
	case <-originalSeen:
	case <-time.After(time.Second):
		t.Fatal("traffic did not reach the current pool during replacement warm-up")
	}
	close(releaseProbe)
	require.NoError(t, <-reloadDone)
	select {
	case <-replacementSeen:
	case <-time.After(time.Second):
		t.Fatal("traffic did not reach the replacement pool after commit")
	}
	stopTraffic()
	traffic.Wait()
	close(errors)
	for failure := range errors {
		t.Errorf("request failed during reload: %s", failure)
	}
	assert.Greater(t, completed.Load(), int64(0))
	assert.Equal(t, "replacement", pool.GetBackends()[0].ID())
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
