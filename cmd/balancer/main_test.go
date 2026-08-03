package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/J0es1ick/test-assignment/internal/config"
	"github.com/J0es1ick/test-assignment/internal/ratelimit"
	"github.com/J0es1ick/test-assignment/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyReloadUpdatesRunningComponents(t *testing.T) {
	current := reloadTestConfig()
	next := reloadTestConfig()
	next.Backends = []string{"http://127.0.0.1:2"}
	next.RateLimit.DefaultCapacity = 4
	next.RateLimit.DefaultRate = 2 * time.Second
	next.HealthCheck.Interval = 10 * time.Second
	next.HealthCheck.Timeout = 10 * time.Millisecond
	next.Server.TrustedProxies = []string{"192.0.2.0/24"}

	pool, err := balancer.NewBackendPool(current.Backends)
	require.NoError(t, err)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	storage := &reloadStorage{}
	limiter := ratelimit.NewTokenBucketLimiter(
		current.RateLimit.DefaultCapacity,
		current.RateLimit.DefaultRate,
		storage,
	)
	healthChecker := balancer.NewHealthChecker(
		pool,
		current.HealthCheck.Interval,
		current.HealthCheck.Timeout,
	)
	httpServer, err := server.NewServer(serverOptions(current), loadBalancer, limiter)
	require.NoError(t, err)

	require.NoError(t, applyReload(
		context.Background(),
		current,
		next,
		pool,
		healthChecker,
		limiter,
		httpServer,
	))

	require.Len(t, pool.GetBackends(), 1)
	assert.Equal(t, "127.0.0.1:2", pool.GetBackends()[0].URL.Host)
	interval, timeout := healthChecker.Settings()
	assert.Equal(t, 10*time.Second, interval)
	assert.Equal(t, 10*time.Millisecond, timeout)

	request := httptest.NewRequest(http.MethodGet, "/api/dashboard/status", nil)
	request.RemoteAddr = "192.0.2.10:40000"
	request.Header.Set("X-Forwarded-For", "203.0.113.15")
	recorder := httptest.NewRecorder()
	httpServer.Handler().ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var status struct {
		ClientIP       string                `json:"client_ip"`
		HealthInterval string                `json:"health_interval"`
		HealthTimeout  string                `json:"health_timeout"`
		Bucket         ratelimit.BucketState `json:"bucket"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
	assert.Equal(t, "203.0.113.15", status.ClientIP)
	assert.Equal(t, "10s", status.HealthInterval)
	assert.Equal(t, "10ms", status.HealthTimeout)
	assert.Equal(t, 4, status.Bucket.Capacity)
	assert.Equal(t, "2s", status.Bucket.Rate)
}

func reloadTestConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Port:              "8080",
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
			ShutdownTimeout:   time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		Backends: []string{"http://127.0.0.1:1"},
		RateLimit: config.RateLimitConfig{
			DefaultCapacity: 10,
			DefaultRate:     time.Second,
		},
		HealthCheck: config.HealthCheckConfig{
			Interval: 5 * time.Second,
			Timeout:  10 * time.Millisecond,
		},
	}
}

type reloadStorage struct {
	mu      sync.Mutex
	buckets map[string]*ratelimit.TokenBucket
}

func (s *reloadStorage) Get(_ context.Context, key string) (*ratelimit.TokenBucket, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, exists := s.buckets[key]
	return bucket, exists, nil
}

func (s *reloadStorage) Set(_ context.Context, key string, bucket *ratelimit.TokenBucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets == nil {
		s.buckets = make(map[string]*ratelimit.TokenBucket)
	}
	s.buckets[key] = bucket
	return nil
}

func (s *reloadStorage) Update(
	_ context.Context,
	key string,
	update func(*ratelimit.TokenBucket) (*ratelimit.TokenBucket, error),
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets == nil {
		s.buckets = make(map[string]*ratelimit.TokenBucket)
	}
	bucket, err := update(s.buckets[key])
	if err != nil {
		return err
	}
	s.buckets[key] = bucket
	return nil
}

func (s *reloadStorage) Reconfigure(_ context.Context, capacity int, rate time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets = make(map[string]*ratelimit.TokenBucket)
	return nil
}
