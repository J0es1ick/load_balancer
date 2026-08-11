package balancer_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBalancerRetriesSafeRequestOnAnotherBackend(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failed.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { _, _ = writer.Write([]byte("OK")) }))
	defer healthy.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "failed", URL: failed.URL}, {ID: "healthy", URL: healthy.URL}}, balancer.PassivePolicy{FailureThreshold: 1, Cooldown: time.Minute})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 2, PerTryTimeout: time.Second, Methods: []string{"GET"}, Statuses: []int{503}}})
	recorder := httptest.NewRecorder()
	lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "OK", recorder.Body.String())
	assert.Equal(t, "healthy", recorder.Header().Get("X-Balancer-Backend"))
	assert.Equal(t, "2", recorder.Header().Get("X-Balancer-Attempts"))
	assert.False(t, pool.GetBackends()[0].IsAlive())
}

func TestRetryDoesNotWaitForStalledResponseBody(t *testing.T) {
	responseStarted := make(chan struct{})
	failed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
		writer.(http.Flusher).Flush()
		close(responseStarted)
		<-request.Context().Done()
	}))
	defer failed.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "OK")
	}))
	defer healthy.Close()

	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "failed", URL: failed.URL}, {ID: "healthy", URL: healthy.URL}}, balancer.PassivePolicy{FailureThreshold: 10, Cooldown: time.Minute, MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 2, PerTryTimeout: time.Second, Methods: []string{"GET"}, Statuses: []int{503}, BudgetCapacity: 10, BudgetRefillPerSecond: 1}})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		done <- recorder
	}()
	select {
	case <-responseStarted:
	case <-time.After(time.Second):
		t.Fatal("retryable response headers were not received")
	}
	select {
	case recorder := <-done:
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "OK", recorder.Body.String())
	case <-time.After(500 * time.Millisecond):
		t.Fatal("retry waited for the stalled response body")
	}
	for _, backend := range pool.GetBackends() {
		assert.Zero(t, backend.Inflight())
	}
}

func TestLoadBalancerPreservesEncodedPath(t *testing.T) {
	requests := make(chan string, 4)
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.RequestURI
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backendServer.Close()
	proxyServer := httptest.NewServer(readyLoadBalancerWithPolicy(t, backendServer.URL+"/base", time.Second))
	defer proxyServer.Close()

	for _, requestPath := range []string{"/foo%2Fbar", "/foo%2Ebar", "/foo%252Fbar", "/foo/%2E%2E/bar"} {
		response, err := http.Get(proxyServer.URL + requestPath) //nolint:gosec -- local test server
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		assert.Equal(t, "/base"+requestPath, <-requests)
	}
}

func TestLoadBalancerStreamsResponseBeforeBackendCompletes(t *testing.T) {
	release := make(chan struct{})
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "first\n")
		writer.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(writer, "second\n")
	}))
	defer backendServer.Close()
	lb := readyLoadBalancerWithPolicy(t, backendServer.URL, 20*time.Millisecond)
	proxyServer := httptest.NewServer(lb)
	defer proxyServer.Close()

	response, err := http.Get(proxyServer.URL) //nolint:gosec -- local test server
	require.NoError(t, err)
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "first\n", first)
	time.Sleep(35 * time.Millisecond)
	close(release)
	second, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "second\n", second)
	require.NoError(t, response.Body.Close())
}

func TestLoadBalancerPreservesProtocolUpgrade(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprint(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		if buffered.Flush() != nil {
			return
		}
		line, err := buffered.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(buffered, "echo:%s", line)
		_ = buffered.Flush()
	}))
	defer backendServer.Close()
	proxyServer := httptest.NewServer(readyLoadBalancerWithPolicy(t, backendServer.URL, 20*time.Millisecond))
	defer proxyServer.Close()
	parsed, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)
	connection, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))
	_, _ = fmt.Fprintf(connection, "GET /socket HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", parsed.Host)
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, status, "101")
	for {
		line, readErr := reader.ReadString('\n')
		require.NoError(t, readErr)
		if line == "\r\n" {
			break
		}
	}
	time.Sleep(35 * time.Millisecond)
	_, _ = io.WriteString(connection, "ping\n")
	echo, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "echo:ping\n", echo)
}

func TestLoadBalancerPropagatesClientCancellation(t *testing.T) {
	backendStarted := make(chan struct{})
	backendCancelled := make(chan struct{})
	backendServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(backendStarted)
		<-request.Context().Done()
		close(backendCancelled)
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}}, balancer.PassivePolicy{FailureThreshold: 1, Cooldown: time.Minute, MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	proxyServer := httptest.NewServer(balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy()))
	defer proxyServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyServer.URL, nil)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		assert.Error(t, requestErr)
		close(done)
	}()
	select {
	case <-backendStarted:
	case <-time.After(time.Second):
		t.Fatal("backend request did not start")
	}
	cancel()
	select {
	case <-backendCancelled:
	case <-time.After(time.Second):
		t.Fatal("backend request was not cancelled")
	}
	<-done
	assert.True(t, pool.GetBackends()[0].IsAlive(), "a client disconnect must not eject a healthy backend")
}

func TestPerTryHeaderTimeoutEjectsSlowBackend(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "slow", URL: backendServer.URL}}, balancer.PassivePolicy{FailureThreshold: 1, Cooldown: time.Minute, MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 1, PerTryTimeout: 15 * time.Millisecond, Methods: []string{"GET"}, BudgetCapacity: 10, BudgetRefillPerSecond: 1}})

	recorder := httptest.NewRecorder()
	lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.False(t, pool.GetBackends()[0].IsAlive(), "an internal upstream timeout must count as a passive failure")
}

func TestRetryableResponseIsPreservedWhenNoAlternativeExists(t *testing.T) {
	var requests atomic.Int64
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "only", URL: backendServer.URL}}, balancer.PassivePolicy{FailureThreshold: 10, Cooldown: time.Minute, MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 2, PerTryTimeout: time.Second, Methods: []string{"GET"}, Statuses: []int{503}, BudgetCapacity: 7, BudgetRefillPerSecond: 0.0001}})
	before := lb.RetryBudget().Tokens

	recorder := httptest.NewRecorder()
	lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "temporarily unavailable")
	assert.Equal(t, "1", recorder.Header().Get("X-Balancer-Attempts"))
	assert.Equal(t, int64(1), requests.Load())
	assert.InDelta(t, before, lb.RetryBudget().Tokens, 0.001, "a retry that cannot start must not spend budget")
}

func readyLoadBalancerWithPolicy(t *testing.T, backendURL string, perTryTimeout time.Duration) *balancer.LoadBalancer {
	t.Helper()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendURL}})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	return balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 1, PerTryTimeout: perTryTimeout, Methods: []string{"GET"}, BudgetCapacity: 10, BudgetRefillPerSecond: 1}})
}

func TestRetryBudgetStopsRetryAmplification(t *testing.T) {
	var healthyRequests atomic.Int64
	failedOne := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer failedOne.Close()
	failedTwo := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer failedTwo.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		healthyRequests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "failed-1", URL: failedOne.URL}, {ID: "failed-2", URL: failedTwo.URL}, {ID: "healthy", URL: healthy.URL}}, balancer.PassivePolicy{FailureThreshold: 10, Cooldown: time.Second, MaxConcurrentRequests: 10, SlowStartMinimum: 100})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy(), balancer.LoadBalancerOptions{Transport: http.DefaultTransport, Retry: balancer.RetryPolicy{MaxAttempts: 3, PerTryTimeout: time.Second, Methods: []string{"GET"}, Statuses: []int{503}, BudgetCapacity: 1, BudgetRefillPerSecond: 0.0001}})
	recorder := httptest.NewRecorder()
	lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "2", recorder.Header().Get("X-Balancer-Attempts"))
	assert.Equal(t, "exhausted", recorder.Header().Get("X-Balancer-Retry-Budget"))
	assert.Zero(t, healthyRequests.Load())
}

func TestBackendConcurrencyLimitRejectsAdditionalAttempt(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	backendServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: backendServer.URL}}, balancer.PassivePolicy{FailureThreshold: 2, Cooldown: time.Second, MaxConcurrentRequests: 1, SlowStartMinimum: 100})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		recorder := httptest.NewRecorder()
		lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-started
	second := httptest.NewRecorder()
	lb.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, second.Code)
	assert.Equal(t, int64(1), pool.GetBackends()[0].Inflight())
	close(release)
	<-firstDone
	assert.Zero(t, pool.GetBackends()[0].Inflight())
}

func TestLoadBalancerReturns503WithoutAvailableBackend(t *testing.T) {
	pool, err := balancer.NewBackendPool(nil)
	require.NoError(t, err)
	lb := balancer.NewLoadBalancer(pool, balancer.NewRoundRobinStrategy())
	recorder := httptest.NewRecorder()
	lb.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
