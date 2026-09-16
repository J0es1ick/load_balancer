package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryTargetsRequireExplicitPlaintextOptIn(t *testing.T) {
	_, err := discoveryTargets("http://127.0.0.1:9090", false)
	require.Error(t, err)
	targets, err := discoveryTargets("https://proxy.internal:9090", false)
	require.NoError(t, err)
	assert.Len(t, targets, 1)
	_, err = discoveryTargets("https://user:secret@proxy.internal", false)
	require.Error(t, err)
	_, err = discoveryTargets("http://127.0.0.1:9090", true)
	require.NoError(t, err)
}

func TestDiscoveryRetainsLastGoodOnErrorAndBroadcastsInOrder(t *testing.T) {
	t.Setenv("BALANCER_DISCOVERY_TOKEN", "test-only")
	t.Setenv("DISCOVERY_RESOLVE_TARGETS", "false")
	good := server.DiscoveryUpdate{Settings: config.GatewayDiscoveryConfig{Type: "dns"}, ObservedAt: time.Now(), Endpoints: []config.GatewayEndpointConfig{{ID: "one", URL: "http://127.0.0.1:8080"}}}
	state := discoveryState{}.observe(good).observe(server.DiscoveryUpdate{Settings: good.Settings, ObservedAt: time.Now(), Error: "DNS unavailable"})
	require.NotNil(t, state.Good)
	require.NotNil(t, state.Failure)
	observed := make(chan server.DiscoveryUpdate, 2)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-only", r.Header.Get("Authorization"))
		assert.Equal(t, "1", r.Header.Get("X-Balancer-CSRF"))
		var update server.DiscoveryUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			t.Error(err)
		}
		observed <- update
		w.WriteHeader(200)
	}))
	defer endpoint.Close()
	target, err := url.Parse(endpoint.URL)
	require.NoError(t, err)
	broadcastDiscovery(context.Background(), []*url.URL{target}, map[string]discoveryState{"api": state})
	assert.Len(t, (<-observed).Endpoints, 1)
	assert.Equal(t, "DNS unavailable", (<-observed).Error)
	state = state.observe(good)
	assert.Nil(t, state.Failure)
	state = state.observe(server.DiscoveryUpdate{Settings: good.Settings, ObservedAt: time.Now(), Endpoints: []config.GatewayEndpointConfig{}})
	require.NotNil(t, state.Good)
	assert.Empty(t, state.Good.Endpoints)
}
