package gateway_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayMatchesRewritesAndHidesDiagnostics(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/v1/users", request.URL.Path)
		assert.Equal(t, "upstream.internal", request.Host)
		assert.Equal(t, "edge", request.Header.Get("X-Source"))
		writer.Header().Set("X-Remove-Me", "secret")
		_, _ = io.WriteString(writer, "ok")
	}))
	defer backend.Close()
	cfg := singleClusterConfig(backend.URL)
	cfg.Routes[0].Match.PathPrefix = "/api"
	cfg.Routes[0].Action.RewritePrefix = "/v1"
	cfg.Routes[0].Action.PreserveHost = false
	cfg.Routes[0].Action.HostRewrite = "upstream.internal"
	cfg.Routes[0].RequestHeaders.Set = map[string]string{"X-Source": "edge"}
	cfg.Routes[0].ResponseHeaders.Remove = []string{"X-Remove-Me"}
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()

	plain := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	assert.Equal(t, http.StatusOK, plain.Code)
	assert.Empty(t, plain.Header().Get("X-Balancer-Backend"))
	assert.Empty(t, plain.Header().Get("X-Remove-Me"))

	diagnostic := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	request = request.WithContext(gateway.WithDiagnostics(request.Context()))
	engine.Handler("public").ServeHTTP(diagnostic, request)
	assert.Equal(t, "root", diagnostic.Header().Get("X-Balancer-Route"))
	assert.Equal(t, "api", diagnostic.Header().Get("X-Balancer-Cluster"))
	assert.Equal(t, "api-1", diagnostic.Header().Get("X-Balancer-Backend"))
}

func TestGatewayRewritePreservesEscapedPathAndSanitizesProxyHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/v1/a%2Fb", request.RequestURI)
		assert.Equal(t, "go-load-balancer", request.Header.Get("X-Balancer-Proxy"))
		assert.Equal(t, "api-1", request.Header.Get("X-Balancer-Backend-Attempt"))
		assert.Empty(t, request.Header.Get("X-Forwarded-Port"))
		assert.Empty(t, request.Header.Get("X-Real-IP"))
		_, _ = io.WriteString(writer, "ok")
	}))
	defer backend.Close()
	cfg := singleClusterConfig(backend.URL)
	cfg.Routes[0].Match.PathPrefix = "/api"
	cfg.Routes[0].Action.RewritePrefix = "/v1"
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()

	request := httptest.NewRequest(http.MethodGet, "/api/a%2Fb", nil)
	request.Header.Set("X-Balancer-Proxy", "client-spoof")
	request.Header.Set("X-Balancer-Backend-Attempt", "client-spoof")
	request.Header.Set("X-Forwarded-Port", "444")
	request.Header.Set("X-Real-IP", "203.0.113.7")
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestGatewayRejectsAmbiguousRequestFraming(t *testing.T) {
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer backend.Close()
	engine, err := gateway.New(context.Background(), singleClusterConfig(backend.URL), testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	request.TransferEncoding = []string{"chunked"}
	request.ContentLength = 4
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Zero(t, calls.Load())
}

func TestGatewayValidateIsPureAndDoesNotProbeEndpoints(t *testing.T) {
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	engine, err := gateway.New(context.Background(), singleClusterConfig(backend.URL), testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	calls.Store(0)

	validated := engine.Config()
	validated.Clusters[0].Health = config.GatewayHealthCheckConfig{Enabled: true, Mode: "http", Path: "/health", Interval: config.Duration(time.Second), Timeout: config.Duration(time.Second), FailureThreshold: 1, SuccessThreshold: 1, MaxConcurrency: 1, ExpectedStatuses: []int{200}, SlowStartMinimum: 100}
	validated.Clusters[0].Endpoints[0].URL = "https://" + strings.TrimPrefix(backend.URL, "http://")
	validated.Clusters[0].TLS.CAFile = "definitely-not-readable.pem"
	require.NoError(t, engine.Validate(context.Background(), validated))
	assert.Zero(t, calls.Load(), "schema validation must not create transports or health probes")
}

func TestGatewayRedirectOnlyNeedsNoClusterAndIsReady(t *testing.T) {
	cfg := &config.GatewayConfig{
		APIVersion: config.GatewayAPIVersion,
		Listeners:  []config.GatewayListenerConfig{{ID: "public", Address: ":8080", Protocol: "http1", Default: true}},
		Routes: []config.GatewayRouteConfig{{
			ID: "redirect", Listener: "public", Match: config.GatewayRouteMatch{PathPrefix: "/"},
			Action: config.GatewayRouteAction{Redirect: &config.GatewayRedirectConfig{Scheme: "https", Host: "example.com"}},
		}},
	}
	cfg.Routes[0].ResponseHeaders.Set = map[string]string{"X-Route": "redirect"}
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	assert.True(t, engine.Ready())
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/docs", nil))
	assert.Equal(t, http.StatusTemporaryRedirect, recorder.Code)
	assert.Equal(t, "https://example.com/docs", recorder.Header().Get("Location"))
	assert.Equal(t, "redirect", recorder.Header().Get("X-Route"))
}

func TestGatewayApplyIfRevisionRejectsStaleUpdate(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
	defer backend.Close()
	engine, err := gateway.New(context.Background(), singleClusterConfig(backend.URL), testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	before := engine.Status().Revision
	updated := engine.Config()
	require.NoError(t, engine.Apply(context.Background(), updated))
	err = engine.ApplyIfRevision(context.Background(), updated, before)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gateway.ErrRevisionConflict))
	assert.Equal(t, before+1, engine.Status().Revision)
	err = engine.RollbackIfRevision(context.Background(), before, before)
	assert.ErrorIs(t, err, gateway.ErrRevisionConflict)
	require.NoError(t, engine.RollbackIfRevision(context.Background(), before, engine.Status().Revision))
	assert.Equal(t, before+2, engine.Status().Revision)
}

func TestGatewayWeightedClustersAndRouteTimeout(t *testing.T) {
	one := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "one") }))
	defer one.Close()
	three := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow" {
			<-request.Context().Done()
			return
		}
		_, _ = io.WriteString(writer, "three")
	}))
	defer three.Close()
	cfg := singleClusterConfig(one.URL)
	cfg.Clusters = append(cfg.Clusters, clusterConfig("three", "three-1", three.URL))
	cfg.Routes[0].Action.Cluster = ""
	cfg.Routes[0].Action.WeightedClusters = []config.GatewayWeightedClusterConfig{{Cluster: "api", Weight: 1}, {Cluster: "three", Weight: 3}}
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	counts := map[string]int{}
	for range 40 {
		recorder := httptest.NewRecorder()
		engine.Handler("public").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		counts[recorder.Body.String()]++
	}
	assert.Equal(t, 10, counts["one"])
	assert.Equal(t, 30, counts["three"])

	timeoutConfig := engine.Config()
	require.NotNil(t, timeoutConfig)
	timeoutConfig.Routes[0].Action.WeightedClusters = nil
	timeoutConfig.Routes[0].Action.Cluster = "three"
	timeoutConfig.Routes[0].Timeouts.Request = config.Duration(20 * time.Millisecond)
	err = engine.Apply(context.Background(), timeoutConfig)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/slow", nil))
	assert.Equal(t, http.StatusGatewayTimeout, recorder.Code)
}

func TestGatewayRequestTimeoutCancelsStreamingResponse(t *testing.T) {
	backendCancelled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "first\n")
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(backendCancelled)
	}))
	defer backend.Close()
	cfg := singleClusterConfig(backend.URL)
	cfg.Routes[0].Timeouts.Request = config.Duration(50 * time.Millisecond)
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	proxy := httptest.NewServer(engine.Handler("public"))
	defer proxy.Close()

	response, err := proxy.Client().Get(proxy.URL)
	require.NoError(t, err)
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "first\n", first)
	select {
	case <-backendCancelled:
	case <-time.After(time.Second):
		t.Fatal("route deadline did not cancel the streaming upstream")
	}
	_, err = reader.ReadString('\n')
	assert.Error(t, err)
	_ = response.Body.Close()
}

func TestGatewayPreservesProtocolUpgrade(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, buffered, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprint(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		if buffered.Flush() != nil {
			return
		}
		line, err := buffered.ReadString('\n')
		if err == nil {
			_, _ = fmt.Fprintf(buffered, "echo:%s", line)
			_ = buffered.Flush()
		}
	}))
	defer backend.Close()
	engine, err := gateway.New(context.Background(), singleClusterConfig(backend.URL), testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	proxy := httptest.NewServer(engine.Handler("public"))
	defer proxy.Close()

	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(proxy.URL, "http://"), time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))
	_, _ = fmt.Fprintf(connection, "GET /socket HTTP/1.1\r\nHost: gateway.test\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
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
	_, _ = io.WriteString(connection, "ping\n")
	echo, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "echo:ping\n", echo)
}

func TestGatewaySupportsH2CUpstream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	upstream := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, 2, request.ProtoMajor)
		_, _ = io.WriteString(writer, "h2c")
	}), Protocols: protocols}
	go func() { _ = upstream.Serve(listener) }()
	t.Cleanup(func() { _ = upstream.Close() })

	cfg := singleClusterConfig("http://" + listener.Addr().String())
	cfg.Clusters[0].Transport.Protocol = "h2c"
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "h2c", recorder.Body.String())
}

func TestGatewayApplyRollbackAndAuthoritativeEmptyDiscovery(t *testing.T) {
	one := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "one") }))
	defer one.Close()
	two := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "two") }))
	defer two.Close()
	cfg := singleClusterConfig(one.URL)
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	initial := engine.Status()

	updated := engine.Config()
	updated.Clusters[0].Endpoints[0].URL = two.URL
	require.NoError(t, engine.Apply(context.Background(), updated))
	afterApply := engine.Status()
	assert.Equal(t, uint64(2), afterApply.Revision)
	assert.Equal(t, initial.Revision, afterApply.PreviousRevision)
	require.NoError(t, engine.Rollback(context.Background(), initial.Revision))
	afterRollback := engine.Status()
	assert.Equal(t, uint64(3), afterRollback.Revision)
	assert.Equal(t, initial.Hash, afterRollback.Hash)
	assertResponseBody(t, engine.Handler("public"), "one")

	discoveryConfig := engine.Config()
	discoveryConfig.Clusters[0].Discovery = config.GatewayDiscoveryConfig{Type: "dns", Hostname: "service.local", Port: 80, Scheme: "http", RefreshInterval: config.Duration(time.Second), StaleAfter: config.Duration(time.Minute)}
	require.NoError(t, engine.Apply(context.Background(), discoveryConfig))
	status := gateway.DiscoveryStatus{Type: "dns", LastUpdate: time.Now().UTC()}
	revisionWithEndpoint := engine.Status().Revision
	require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", engine.Config().Clusters[0].Endpoints, status))
	assert.Equal(t, revisionWithEndpoint, engine.Status().Revision, "identical discovery delivery must not rebuild the pool")
	require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", []config.GatewayEndpointConfig{}, status))
	emptyRevision := engine.Status().Revision
	refreshed := gateway.DiscoveryStatus{Type: "dns", LastUpdate: time.Now().UTC().Add(time.Second)}
	require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", []config.GatewayEndpointConfig{}, refreshed))
	assert.Equal(t, emptyRevision, engine.Status().Revision)
	assert.Equal(t, refreshed.LastUpdate, engine.Status().Clusters[0].Discovery.LastUpdate)
	err = engine.UpdateEndpoints(context.Background(), "api", nil, gateway.DiscoveryStatus{Type: "dns", LastUpdate: status.LastUpdate.Add(-time.Second)})
	assert.ErrorIs(t, err, gateway.ErrStaleDiscoveryUpdate)
	wrongSource := engine.Config().Clusters[0].Discovery
	wrongSource.Hostname = "replacement.local"
	err = engine.UpdateEndpointsForSource(context.Background(), "api", nil, refreshed, wrongSource)
	assert.ErrorIs(t, err, gateway.ErrDiscoverySourceChanged)
	err = engine.MarkDiscoveryErrorForSource("api", errors.New("late provider error"), true, wrongSource)
	assert.ErrorIs(t, err, gateway.ErrDiscoverySourceChanged)
	assert.Empty(t, engine.Status().Clusters[0].Discovery.Error)
	assert.False(t, engine.Ready())
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Empty(t, engine.Status().Clusters[0].Endpoints)
	require.NoError(t, engine.UpdateDiscoveryStatus("api", gateway.DiscoveryStatus{Type: "dns", LastUpdate: time.Now().Add(-2 * time.Minute)}))
	assert.True(t, engine.Status().Clusters[0].Discovery.Stale, "staleness must be derived even without an explicit provider error")
}

func TestGatewayConcurrentApplyNeverPublishesAnUnwarmedGap(t *testing.T) {
	one := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "one") }))
	defer one.Close()
	two := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "two") }))
	defer two.Close()
	engine, err := gateway.New(context.Background(), singleClusterConfig(one.URL), testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	var wait sync.WaitGroup
	errorsChannel := make(chan int, 200)
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range 100 {
			recorder := httptest.NewRecorder()
			engine.Handler("public").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			if recorder.Code != http.StatusOK {
				errorsChannel <- recorder.Code
			}
		}
	}()
	for index := range 20 {
		updated := engine.Config()
		if index%2 == 0 {
			updated.Clusters[0].Endpoints[0].URL = two.URL
		} else {
			updated.Clusters[0].Endpoints[0].URL = one.URL
		}
		require.NoError(t, engine.Apply(context.Background(), updated))
	}
	wait.Wait()
	close(errorsChannel)
	assert.Empty(t, errorsChannel)
}

func TestGatewayRejectsChunkedBodyAboveRouteLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	cfg := singleClusterConfig(backend.URL)
	cfg.Routes[0].MaxRequestBodyBytes = 3
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	request := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader("oversized")))
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	recorder := httptest.NewRecorder()
	engine.Handler("public").ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Equal(t, uint64(1), engine.Status().Stats.BodyRejected)
}

func TestGatewayRejectsUnhealthyReplacementAndKeepsLastGoodSnapshot(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if request.URL.Path != "/health" {
			_, _ = io.WriteString(writer, "healthy")
		}
	}))
	defer healthy.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer unhealthy.Close()
	cfg := singleClusterConfig(healthy.URL)
	cfg.Clusters[0].Health = config.GatewayHealthCheckConfig{Enabled: true, Mode: "http", Path: "/health", Interval: config.Duration(time.Second), Timeout: config.Duration(100 * time.Millisecond), FailureThreshold: 1, SuccessThreshold: 1, MaxConcurrency: 1, ExpectedStatuses: []int{200}, SlowStartMinimum: 100}
	engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	before := engine.Status()
	updated := engine.Config()
	updated.Clusters[0].Endpoints[0].URL = unhealthy.URL
	err = engine.Apply(context.Background(), updated)
	require.Error(t, err)
	assert.Equal(t, before.Revision, engine.Status().Revision)
	assertResponseBody(t, engine.Handler("public"), "healthy")
}

func singleClusterConfig(backendURL string) *config.GatewayConfig {
	return &config.GatewayConfig{APIVersion: config.GatewayAPIVersion, HistoryLimit: 10, Listeners: []config.GatewayListenerConfig{{ID: "public", Address: ":8080", Protocol: "http1", Default: true}}, Routes: []config.GatewayRouteConfig{{ID: "root", Listener: "public", Match: config.GatewayRouteMatch{PathPrefix: "/"}, Action: config.GatewayRouteAction{Cluster: "api", PreserveHost: true}}}, Clusters: []config.GatewayClusterConfig{clusterConfig("api", "api-1", backendURL)}}
}

func clusterConfig(id, endpointID, backendURL string) config.GatewayClusterConfig {
	return config.GatewayClusterConfig{ID: id, Strategy: "round_robin", Endpoints: []config.GatewayEndpointConfig{{ID: endpointID, URL: backendURL, Weight: 1}}, Discovery: config.GatewayDiscoveryConfig{Type: "static"}}
}

func testDefaults() gateway.Defaults {
	return gateway.Defaults{Retry: balancer.RetryPolicy{MaxAttempts: 1, PerTryTimeout: time.Second, BodyLimit: 1024, Methods: []string{"GET"}, BudgetCapacity: 10, BudgetRefillPerSecond: 1}, WarmupTimeout: time.Second}
}

func assertResponseBody(t *testing.T, handler http.Handler, expected string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, expected, recorder.Body.String())
}
