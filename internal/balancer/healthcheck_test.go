package balancer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPHealthCheckerUsesApplicationStatus(t *testing.T) {
	status := http.StatusOK
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(status) }))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}})
	require.NoError(t, err)
	settings := balancer.HealthSettings{Mode: "http", Path: "/health", Interval: time.Second, Timeout: time.Second, FailureThreshold: 2, SuccessThreshold: 1, MaxConcurrency: 2, ExpectedStatuses: []int{200}}
	checker, err := balancer.NewHealthChecker(pool, settings)
	require.NoError(t, err)
	checker.Check(context.Background())
	assert.True(t, pool.GetBackends()[0].IsAlive())
	status = http.StatusInternalServerError
	checker.Check(context.Background())
	assert.True(t, pool.GetBackends()[0].IsAlive(), "one failure is below threshold")
	checker.Check(context.Background())
	assert.False(t, pool.GetBackends()[0].IsAlive())
}

func TestHealthCheckerSchedulerDoesNotOverlapSlowCycles(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	started := make(chan struct{}, 1)
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-time.After(30 * time.Millisecond):
			writer.WriteHeader(http.StatusOK)
		case <-request.Context().Done():
		}
	}))
	defer backendServer.Close()

	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "slow", URL: backendServer.URL}})
	require.NoError(t, err)
	checker, err := balancer.NewHealthChecker(pool, balancer.HealthSettings{Mode: "http", Path: "/health", Interval: 5 * time.Millisecond, Timeout: 100 * time.Millisecond, FailureThreshold: 1, SuccessThreshold: 1, MaxConcurrency: 1, ExpectedStatuses: []int{200}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.Start(ctx)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("health cycle did not start")
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health scheduler did not stop after cancellation")
	}
	assert.Equal(t, int32(1), maximum.Load())
}
