package balancer_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWeightedRoundRobinUsesEndpointWeights(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "one", URL: "http://one", Weight: 1}, {ID: "three", URL: "http://three", Weight: 3}})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	strategy := balancer.NewWeightedRoundRobinStrategy()
	counts := map[string]int{}
	for range 40 {
		counts[strategy.GetNextPeer(pool).ID()]++
	}
	assert.Equal(t, 10, counts["one"])
	assert.Equal(t, 30, counts["three"])
}

func TestLeastRequestSkipsLoadedEndpoint(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "busy", URL: "http://busy"}, {ID: "idle", URL: "http://idle"}}, balancer.PassivePolicy{MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	require.True(t, pool.GetBackends()[0].TryAcquire())
	defer pool.GetBackends()[0].Release()
	assert.Equal(t, "idle", balancer.NewLeastRequestStrategy().GetNextPeer(pool).ID())
}

func TestRendezvousIsStableAndHonorsExclusion(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "one", URL: "http://one"}, {ID: "two", URL: "http://two"}, {ID: "three", URL: "http://three"}})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	strategy := balancer.NewRendezvousStrategy(func(request *http.Request) string { return request.Header.Get("X-Key") })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Key", "customer-42")
	first := strategy.GetNextPeerForRequest(pool, nil, request)
	for range 20 {
		assert.Equal(t, first.ID(), strategy.GetNextPeerForRequest(pool, nil, request).ID())
	}
	second := strategy.GetNextPeerForRequest(pool, map[string]struct{}{first.ID(): {}}, request)
	assert.NotNil(t, second)
	assert.NotEqual(t, first.ID(), second.ID())
}

func TestRetryBodyReadFailureIsRejectedInsteadOfForwardingTruncatedBody(t *testing.T) {
	var backendRequests int
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { backendRequests++ }))
	defer backend.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backend.URL}}, balancer.PassivePolicy{MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 2, PerTryTimeout: time.Second, BodyLimit: 32, Methods: []string{"POST"}, BudgetCapacity: 10, BudgetRefillPerSecond: 1}})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.ContentLength = 4
	request.Body = failingBody{}
	recorder := httptest.NewRecorder()
	loadBalancer.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Zero(t, backendRequests)
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("broken body") }
func (failingBody) Close() error             { return nil }

var _ io.ReadCloser = failingBody{}
