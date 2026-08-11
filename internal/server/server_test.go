package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagementPlaneIsAuthenticatedAndUsesRealComponents(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("backend response"))
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	loadBalancer := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: true, Policy: ratelimit.Policy{Capacity: 2, RefillPerSecond: 0.01}, FailureMode: "fail-closed", OperationTimeout: time.Second}, ratelimit.NewLocalStore(4), nil)
	require.NoError(t, err)
	metrics := observability.NewMetrics()
	httpServer, err := server.NewServer(testOptions(metrics), loadBalancer, limiter)
	require.NoError(t, err)

	unauthorized := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(unauthorized, dashboardRequest(http.MethodGet, "/api/dashboard/status", "", false))
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	statusRecorder := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(statusRecorder, dashboardRequest(http.MethodGet, "/api/dashboard/status", "", true))
	require.Equal(t, http.StatusOK, statusRecorder.Code)
	var status struct {
		ClientIP         string                     `json:"client_ip"`
		InstanceID       string                     `json:"instance_id"`
		RuntimeMutations bool                       `json:"runtime_mutations_enabled"`
		Bucket           ratelimit.BucketState      `json:"bucket"`
		Backends         []balancer.BackendSnapshot `json:"backends"`
		Storage          string                     `json:"storage"`
		Health           struct {
			Interval string `json:"interval"`
			Timeout  string `json:"timeout"`
		} `json:"health_check"`
		Retry struct {
			MaxAttempts   int    `json:"max_attempts"`
			PerTryTimeout string `json:"per_try_timeout"`
		} `json:"retry"`
	}
	require.NoError(t, json.Unmarshal(statusRecorder.Body.Bytes(), &status))
	assert.Equal(t, "192.0.2.10", status.ClientIP)
	assert.Equal(t, "replica-test", status.InstanceID)
	assert.True(t, status.RuntimeMutations)
	assert.Equal(t, 2.0, status.Bucket.Tokens, "read-only status must not consume a token")
	assert.Equal(t, "api", status.Backends[0].ID)
	assert.Equal(t, "local", status.Storage)
	assert.Equal(t, "1s", status.Health.Interval)
	assert.Equal(t, "1s", status.Health.Timeout)
	assert.Equal(t, 1, status.Retry.MaxAttempts)
	assert.Equal(t, "10s", status.Retry.PerTryTimeout)

	requestRecorder := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(requestRecorder, dashboardRequest(http.MethodGet, "/api/dashboard/request", "", true))
	assert.Equal(t, http.StatusOK, requestRecorder.Code)
	assert.Equal(t, "api", requestRecorder.Header().Get("X-Balancer-Backend"))

	disableRecorder := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(disableRecorder, dashboardRequest(http.MethodPost, "/api/dashboard/backends/api", `{"enabled":false}`, true))
	assert.Equal(t, http.StatusOK, disableRecorder.Code)
	assert.False(t, pool.GetBackends()[0].IsEnabled())

	drainRecorder := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(drainRecorder, dashboardRequest(http.MethodPost, "/api/dashboard/backends/api/drain", "", true))
	assert.Equal(t, http.StatusAccepted, drainRecorder.Code)
	assert.True(t, pool.GetBackends()[0].IsDraining())
}

func TestServerGracefulShutdownStopsBothListeners(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://127.0.0.1:1"}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	options := testOptions(observability.NewMetrics())
	options.Port = "0"
	options.ManagementAddress = "127.0.0.1:0"
	httpServer, err := server.NewServer(options, balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()), limiter)
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- httpServer.Start() }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, httpServer.Shutdown(ctx))
	select {
	case startErr := <-started:
		assert.NoError(t, startErr)
	case <-time.After(time.Second):
		t.Fatal("server start did not return after shutdown")
	}
}

func TestPublicListenerDoesNotTerminateStreamingResponse(t *testing.T) {
	release := make(chan struct{})
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "first\n")
		writer.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(writer, "second\n")
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	options := testOptions(observability.NewMetrics())
	options.ManagementEnabled = false
	options.WriteTimeout = 0
	options.ManagementWriteTimeout = 20 * time.Millisecond
	httpServer, err := server.NewServer(options, balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()), limiter)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener, nil) }()

	response, err := http.Get("http://" + listener.Addr().String())
	require.NoError(t, err)
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "first\n", first)
	time.Sleep(50 * time.Millisecond)
	close(release)
	second, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "second\n", second)
	require.NoError(t, response.Body.Close())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, httpServer.Shutdown(ctx))
	require.NoError(t, <-served)
}

func TestRuntimeMutationsCanBeDisabledForMultiReplicaDeployments(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://127.0.0.1:1"}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 10, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	options := testOptions(observability.NewMetrics())
	options.RuntimeMutations = false
	httpServer, err := server.NewServer(options, balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()), limiter)
	require.NoError(t, err)

	for _, path := range []string{"/api/dashboard/backends/api", "/api/dashboard/backends/api/drain", "/api/dashboard/limit"} {
		recorder := httptest.NewRecorder()
		httpServer.ManagementHandler().ServeHTTP(recorder, dashboardRequest(http.MethodPost, path, `{}`, true))
		assert.Equal(t, http.StatusForbidden, recorder.Code, path)
	}
	configRecorder := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(configRecorder, dashboardRequest(http.MethodPatch, "/api/dashboard/config", `{}`, true))
	assert.Equal(t, http.StatusForbidden, configRecorder.Code)
	assert.True(t, pool.GetBackends()[0].IsEnabled())
}

func TestPublicHealthEndpointsAndTrustedProxyResolution(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://127.0.0.1:1"}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	httpServer, err := server.NewServer(testOptions(observability.NewMetrics()), balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()), limiter)
	require.NoError(t, err)

	liveness := httptest.NewRecorder()
	httpServer.PublicHandler().ServeHTTP(liveness, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, liveness.Code)
	readiness := httptest.NewRecorder()
	httpServer.PublicHandler().ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, readiness.Code)

	request := dashboardRequest(http.MethodGet, "/api/dashboard/status", "", true)
	request.RemoteAddr = "192.0.2.10:41000"
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
	recorder := httptest.NewRecorder()
	httpServer.ManagementHandler().ServeHTTP(recorder, request)
	var response struct {
		ClientIP string `json:"client_ip"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "203.0.113.9", response.ClientIP)
}

func TestPublicProxyRebuildsForwardingHeadersFromTrustedClientIP(t *testing.T) {
	type observedHeaders struct {
		forwardedFor   string
		realIP         string
		forwardedHost  string
		forwardedProto string
		forwarded      string
	}
	observed := make(chan observedHeaders, 2)
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed <- observedHeaders{
			forwardedFor: request.Header.Get("X-Forwarded-For"), realIP: request.Header.Get("X-Real-IP"),
			forwardedHost: request.Header.Get("X-Forwarded-Host"), forwardedProto: request.Header.Get("X-Forwarded-Proto"),
			forwarded: request.Header.Get("Forwarded"),
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	httpServer, err := server.NewServer(testOptions(observability.NewMetrics()), balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()), limiter)
	require.NoError(t, err)

	untrusted := httptest.NewRequest(http.MethodGet, "http://docs.example.test/resource", nil)
	untrusted.RemoteAddr = "198.51.100.7:41000"
	untrusted.Header.Set("X-Forwarded-For", "1.2.3.4")
	untrusted.Header.Set("X-Real-IP", "1.2.3.4")
	untrusted.Header.Set("X-Forwarded-Proto", "https")
	untrusted.Header.Set("Forwarded", "for=1.2.3.4")
	httpServer.PublicHandler().ServeHTTP(httptest.NewRecorder(), untrusted)
	first := <-observed
	assert.Equal(t, "198.51.100.7", first.forwardedFor)
	assert.Equal(t, "198.51.100.7", first.realIP)
	assert.Equal(t, "docs.example.test", first.forwardedHost)
	assert.Equal(t, "http", first.forwardedProto)
	assert.Empty(t, first.forwarded)

	trusted := httptest.NewRequest(http.MethodGet, "http://docs.example.test/resource", nil)
	trusted.RemoteAddr = "192.0.2.10:41000"
	trusted.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
	trusted.Header.Set("X-Forwarded-Proto", "https")
	httpServer.PublicHandler().ServeHTTP(httptest.NewRecorder(), trusted)
	second := <-observed
	assert.Equal(t, "203.0.113.9", second.forwardedFor)
	assert.Equal(t, "203.0.113.9", second.realIP)
	assert.Equal(t, "https", second.forwardedProto)
}

func TestOverloadProtectionBoundsConcurrentRequests(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	options := testOptions(observability.NewMetrics())
	options.OverloadMaxConcurrent = 1
	options.OverloadQueueTimeout = 5 * time.Millisecond
	httpServer, err := server.NewServer(options, balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()), limiter)
	require.NoError(t, err)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		recorder := httptest.NewRecorder()
		httpServer.PublicHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-started
	second := httptest.NewRecorder()
	httpServer.PublicHandler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, second.Code)
	assert.Equal(t, "true", second.Header().Get("X-Balancer-Overloaded"))
	close(release)
	<-firstDone
}

func testOptions(metrics *observability.Metrics) server.Options {
	return server.Options{Port: "8080", ManagementEnabled: true, ManagementAddress: ":9090", ManagementAuthToken: "test-token", RuntimeMutations: true, InstanceID: "replica-test", TrustedProxies: []string{"192.0.2.0/24", "10.0.0.0/8"}, ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, ManagementWriteTimeout: time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 1 << 20, OverloadMaxConcurrent: 64, OverloadQueueTimeout: time.Millisecond, Health: balancer.HealthSettings{Mode: "http", Path: "/health", Interval: time.Second, Timeout: time.Second, FailureThreshold: 2, SuccessThreshold: 1, MaxConcurrency: 2, ExpectedStatuses: []int{200}}, Metrics: metrics}
}

func dashboardRequest(method, path, body string, authenticated bool) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.10:41000"
	if authenticated {
		request.Header.Set("Authorization", "Bearer test-token")
	}
	return request
}
