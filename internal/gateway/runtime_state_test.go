package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayRouteApplyPreservesEndpointDrain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	engine, err := gateway.New(context.Background(), singleClusterConfig(upstream.URL), testDefaults(), nil)
	require.NoError(t, err)
	defer engine.Close()
	require.NoError(t, engine.DrainEndpoint("api", "api-1"))
	cfg := engine.Config()
	cfg.Routes[0].Priority++
	require.NoError(t, engine.Apply(context.Background(), cfg))
	state := engine.Status().Clusters[0].Endpoints[0]
	assert.True(t, state.Draining)
	assert.False(t, state.Available)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestGatewayGenerationsShareInflightAccounting(t *testing.T) {
	for _, update := range []string{"apply", "rollback", "discovery", "remove-and-return", "failed-apply", "canceled-apply"} {
		t.Run(update, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hold" {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					close(started)
					<-release
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()
			defer unblock()
			cfg := singleClusterConfig(upstream.URL)
			cfg.Clusters[0].Transport.MaxConcurrentRequests = 1
			cfg.Clusters[0].Discovery = config.GatewayDiscoveryConfig{Type: "dns", Hostname: "service.local", Port: 80, Scheme: "http"}
			defaults := testDefaults()
			defaults.Retry.PerTryTimeout = 10 * time.Second
			engine, err := gateway.New(context.Background(), cfg, defaults, nil)
			require.NoError(t, err)
			defer engine.Close()
			if update == "rollback" {
				changed := engine.Config()
				changed.Routes[0].Priority++
				require.NoError(t, engine.Apply(context.Background(), changed))
			}
			done := make(chan int, 1)
			go func() {
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hold", nil))
				done <- response.Code
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("first request did not reach upstream")
			}
			assertBlocked := func() {
				t.Helper()
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
				assert.Equal(t, http.StatusServiceUnavailable, response.Code)
			}
			assertBlocked()
			switch update {
			case "apply":
				for range 3 {
					changed := engine.Config()
					changed.Routes[0].Priority++
					require.NoError(t, engine.Apply(context.Background(), changed))
				}
			case "rollback":
				require.NoError(t, engine.Rollback(context.Background(), 1))
			case "discovery":
				endpoints := engine.Config().Clusters[0].Endpoints
				endpoints[0].Weight++
				require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", endpoints, gateway.DiscoveryStatus{Type: "dns"}))
			case "remove-and-return":
				endpoints := engine.Config().Clusters[0].Endpoints
				require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", nil, gateway.DiscoveryStatus{Type: "dns"}))
				require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", endpoints, gateway.DiscoveryStatus{Type: "dns"}))
			case "failed-apply":
				changed := engine.Config()
				changed.Clusters[0].Transport.MaxConcurrentRequests = 2
				bad := clusterConfig("bad", "bad-1", "http://127.0.0.1:1")
				bad.Health.Enabled = true
				changed.Clusters = append(changed.Clusters, bad)
				require.Error(t, engine.Apply(context.Background(), changed))
			case "canceled-apply":
				changed := engine.Config()
				changed.Clusters[0].Transport.MaxConcurrentRequests = 2
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				require.ErrorIs(t, engine.Apply(ctx, changed), context.Canceled)
			}
			assert.Equal(t, int64(1), engine.Status().Clusters[0].Endpoints[0].Inflight)
			assertBlocked()
			unblock()
			select {
			case code := <-done:
				assert.Equal(t, http.StatusOK, code)
			case <-time.After(3 * time.Second):
				t.Fatal("held request did not finish")
			}
			assert.Zero(t, engine.Status().Clusters[0].Endpoints[0].Inflight)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, http.StatusOK, response.Code)
		})
	}
}

func TestGatewayEndpointOverridesAndDeclarationsAcrossUpdates(t *testing.T) {
	for _, healthEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-probes", true: "with-probes"}[healthEnabled], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer upstream.Close()
			cfg := singleClusterConfig(upstream.URL)
			cfg.Clusters[0].Health.Enabled = healthEnabled
			cfg.Clusters[0].Discovery = config.GatewayDiscoveryConfig{Type: "dns", Hostname: "service.local", Port: 80, Scheme: "http"}
			cfg.Clusters[0].Endpoints[0].Disabled = true
			engine, err := gateway.New(context.Background(), cfg, testDefaults(), nil)
			require.NoError(t, err)
			defer engine.Close()
			require.NoError(t, engine.SetEndpoint("api", "api-1", true))
			changed := engine.Config()
			changed.Routes[0].Priority++
			require.NoError(t, engine.Apply(context.Background(), changed))
			assert.True(t, engine.Status().Clusters[0].Endpoints[0].Enabled, "manual reserve activation must survive route changes")
			require.NoError(t, engine.DrainEndpoint("api", "api-1"))
			endpoints := engine.Config().Clusters[0].Endpoints
			endpoints[0].Weight++
			require.NoError(t, engine.UpdateEndpoints(context.Background(), "api", endpoints, gateway.DiscoveryStatus{Type: "dns"}))
			assert.True(t, engine.Status().Clusters[0].Endpoints[0].Draining)
			require.NoError(t, engine.Rollback(context.Background(), 1))
			assert.True(t, engine.Status().Clusters[0].Endpoints[0].Draining, "rollback must not end drain")
			changed = engine.Config()
			changed.Clusters[0].Endpoints[0].Disabled = false
			require.NoError(t, engine.Apply(context.Background(), changed))
			endpoint := engine.Status().Clusters[0].Endpoints[0]
			assert.True(t, endpoint.Available, "explicit disabled change must override manual drain")
			assert.False(t, endpoint.Draining)
			changed.Clusters[0].Endpoints[0].Disabled = true
			require.NoError(t, engine.Apply(context.Background(), changed))
			assert.False(t, engine.Status().Clusters[0].Endpoints[0].Enabled)
		})
	}
}
