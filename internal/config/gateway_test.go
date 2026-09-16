package config_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

func TestGatewayConfigUsesVersionedStrictSchemaAndStringDurations(t *testing.T) {
	input := []byte(`
apiVersion: proxy/v1
listeners:
  - id: public
    address: ":8080"
    protocol: http1
routes:
  - id: api
    listener: public
    match: {path_prefix: /api}
    action: {cluster: api}
    timeouts: {request: 3s, per_try: 1s}
clusters:
  - id: api
    strategy: weighted_round_robin
    discovery: {type: static}
    endpoints:
      - {id: api-1, url: http://backend:8080, weight: 2}
`)
	var gateway config.GatewayConfig
	require.NoError(t, yaml.UnmarshalStrict(input, &gateway))
	gateway.ApplyDefaults()
	require.NoError(t, gateway.Validate())
	assert.Equal(t, 3*time.Second, gateway.Routes[0].Timeouts.Request.Duration())
	encoded, err := json.Marshal(gateway)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"request":"3s"`)
	var decoded config.GatewayConfig
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, time.Second, decoded.Routes[0].Timeouts.PerTry.Duration())
}

func TestGatewayConfigRejectsUnsupportedStrategyAndHopHeaders(t *testing.T) {
	gateway := config.GatewayConfig{APIVersion: config.GatewayAPIVersion, HistoryLimit: 10, Listeners: []config.GatewayListenerConfig{{ID: "public", Address: ":8080", Protocol: "http1"}}, Routes: []config.GatewayRouteConfig{{ID: "route", Action: config.GatewayRouteAction{Cluster: "api"}, RequestHeaders: config.GatewayHeaderPolicy{Set: map[string]string{"Connection": "close"}}}}, Clusters: []config.GatewayClusterConfig{{ID: "api", Strategy: "random", Discovery: config.GatewayDiscoveryConfig{Type: "static"}, Endpoints: []config.GatewayEndpointConfig{{ID: "one", URL: "http://one", Weight: 1}}}}}
	require.ErrorContains(t, gateway.Validate(), "unsupported strategy")
	gateway.Clusters[0].Strategy = "round_robin"
	gateway.ApplyDefaults()
	require.ErrorContains(t, gateway.Validate(), "cannot be rewritten")
}

func TestGatewayConfigCanonicalizesMethodsAndRedirectDefaults(t *testing.T) {
	gateway := config.GatewayConfig{
		Listeners: []config.GatewayListenerConfig{{ID: "public", Address: ":8080"}},
		Routes: []config.GatewayRouteConfig{{
			ID: "redirect", Match: config.GatewayRouteMatch{PathPrefix: "/", Methods: []string{"get"}},
			Action: config.GatewayRouteAction{Redirect: &config.GatewayRedirectConfig{Scheme: "https", Host: "example.com"}},
		}},
	}
	gateway.ApplyDefaults()
	require.NoError(t, gateway.Validate())
	assert.Equal(t, "GET", gateway.Routes[0].Match.Methods[0])
	assert.Equal(t, 307, gateway.Routes[0].Action.Redirect.StatusCode)
}

func TestGatewayConfigRejectsReservedAndMalformedHeaderMutations(t *testing.T) {
	base := config.GatewayConfig{APIVersion: config.GatewayAPIVersion, HistoryLimit: 10, Listeners: []config.GatewayListenerConfig{{ID: "public", Address: ":8080", Protocol: "http1"}}, Routes: []config.GatewayRouteConfig{{ID: "route", Action: config.GatewayRouteAction{Cluster: "api"}}}, Clusters: []config.GatewayClusterConfig{{ID: "api", Strategy: "round_robin", Discovery: config.GatewayDiscoveryConfig{Type: "static"}, Endpoints: []config.GatewayEndpointConfig{{ID: "one", URL: "http://one", Weight: 1}}}}}
	base.ApplyDefaults()
	for _, name := range []string{"X-Balancer-Backend", "X-Forwarded-Port", "Forwarded", "X-Real-IP"} {
		candidate := base
		candidate.Routes = append([]config.GatewayRouteConfig(nil), base.Routes...)
		candidate.Routes[0].RequestHeaders.Set = map[string]string{name: "spoof"}
		require.ErrorContains(t, candidate.Validate(), "cannot be rewritten")
	}
	candidate := base
	candidate.Routes = append([]config.GatewayRouteConfig(nil), base.Routes...)
	candidate.Routes[0].ResponseHeaders.Set = map[string]string{"X-Valid": "one\r\ntwo"}
	require.ErrorContains(t, candidate.Validate(), "line break")
	candidate = base
	candidate.Routes = append([]config.GatewayRouteConfig(nil), base.Routes...)
	candidate.Routes[0].Match.Headers = []config.GatewayHeaderMatchConfig{{Name: "X-Forwarded-For", Exact: "trusted"}}
	require.ErrorContains(t, candidate.Validate(), "cannot be used for route matching")
}

func TestGatewayConfigRejectsInvalidAuthoritiesAndEndpointFragments(t *testing.T) {
	gateway := config.GatewayConfig{APIVersion: config.GatewayAPIVersion, HistoryLimit: 10, Listeners: []config.GatewayListenerConfig{{ID: "public", Address: ":8080", Protocol: "http1"}}, Routes: []config.GatewayRouteConfig{{ID: "route", Action: config.GatewayRouteAction{Cluster: "api", HostRewrite: "backend\r\nInjected: yes"}}}, Clusters: []config.GatewayClusterConfig{{ID: "api", Strategy: "round_robin", Discovery: config.GatewayDiscoveryConfig{Type: "static"}, Endpoints: []config.GatewayEndpointConfig{{ID: "one", URL: "http://one", Weight: 1}}}}}
	gateway.ApplyDefaults()
	require.ErrorContains(t, gateway.Validate(), "host_rewrite")
	gateway.Routes[0].Action.HostRewrite = ""
	gateway.Clusters[0].Endpoints[0].URL = "http://one/path#fragment"
	require.ErrorContains(t, gateway.Validate(), "invalid endpoint")
}

func TestGatewayConfigRequiresExplicitKubernetesServicePort(t *testing.T) {
	gateway := config.GatewayConfig{
		Listeners: []config.GatewayListenerConfig{{ID: "public", Address: ":8080"}},
		Routes:    []config.GatewayRouteConfig{{ID: "route", Action: config.GatewayRouteAction{Cluster: "api"}}},
		Clusters: []config.GatewayClusterConfig{{
			ID: "api", Discovery: config.GatewayDiscoveryConfig{Type: "kubernetes", Namespace: "apps", Service: "api"},
		}},
	}
	gateway.ApplyDefaults()
	require.ErrorContains(t, gateway.Validate(), "explicit port")
	gateway.Clusters[0].Discovery.Port = 8080
	require.NoError(t, gateway.Validate())
}
