package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/J0es1ick/test-assignment/internal/ratelimit"
	"github.com/J0es1ick/test-assignment/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryStorage struct {
	mu     sync.Mutex
	bucket *ratelimit.TokenBucket
}

func (m *memoryStorage) Get(context.Context, string) (*ratelimit.TokenBucket, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bucket, m.bucket != nil, nil
}

func (m *memoryStorage) Set(_ context.Context, _ string, bucket *ratelimit.TokenBucket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bucket = bucket
	return nil
}

func (m *memoryStorage) Update(_ context.Context, _ string, update func(*ratelimit.TokenBucket) (*ratelimit.TokenBucket, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, err := update(m.bucket)
	if err != nil {
		return err
	}
	m.bucket = bucket
	return nil
}

func dashboardRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.10:41000"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func TestDashboardAPIUsesRealBalancerAndLimiter(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/", r.URL.Path)
		_, _ = w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	pool := balancer.NewBackendPool([]string{backend.URL})
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	limiter := ratelimit.NewTokenBucketLimiter(2, time.Second, &memoryStorage{})
	api := server.NewServer("0", loadBalancer, limiter).Handler()

	t.Run("reports live state", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, dashboardRequest(http.MethodGet, "/api/dashboard/status", ""))

		require.Equal(t, http.StatusOK, recorder.Code)
		var status struct {
			Mode     string `json:"mode"`
			Backends []struct {
				Available bool `json:"available"`
			} `json:"backends"`
			Bucket ratelimit.BucketState `json:"bucket"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
		assert.Equal(t, "live", status.Mode)
		assert.True(t, status.Backends[0].Available)
		assert.Equal(t, 2, status.Bucket.Capacity)
	})

	backendID := pool.Backends[0].ID()

	t.Run("disables backend in the actual pool", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, dashboardRequest(
			http.MethodPost,
			"/api/dashboard/backends/"+backendID,
			`{"enabled":false}`,
		))

		require.Equal(t, http.StatusOK, recorder.Code)
		assert.False(t, pool.Backends[0].IsEnabled())

		recorder = httptest.NewRecorder()
		api.ServeHTTP(recorder, dashboardRequest(http.MethodGet, "/api/dashboard/request", ""))
		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	})

	t.Run("resets bucket and exposes proxy metadata", func(t *testing.T) {
		resetRecorder := httptest.NewRecorder()
		api.ServeHTTP(resetRecorder, dashboardRequest(http.MethodPost, "/api/dashboard/limit", `{"capacity":1}`))
		require.Equal(t, http.StatusOK, resetRecorder.Code)

		enableRecorder := httptest.NewRecorder()
		api.ServeHTTP(enableRecorder, dashboardRequest(
			http.MethodPost,
			"/api/dashboard/backends/"+backendID,
			`{"enabled":true}`,
		))
		require.Equal(t, http.StatusOK, enableRecorder.Code)

		first := httptest.NewRecorder()
		api.ServeHTTP(first, dashboardRequest(http.MethodGet, "/api/dashboard/request", ""))
		assert.Equal(t, http.StatusOK, first.Code)
		assert.NotEmpty(t, first.Header().Get("X-Balancer-Backend"))
		assert.Equal(t, "0", first.Header().Get("X-RateLimit-Remaining"))

		second := httptest.NewRecorder()
		api.ServeHTTP(second, dashboardRequest(http.MethodGet, "/api/dashboard/request", ""))
		assert.Equal(t, http.StatusTooManyRequests, second.Code)
	})
}
