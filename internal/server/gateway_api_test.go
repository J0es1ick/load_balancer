package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGatewayServer(t *testing.T, handler http.Handler, configure ...func(*config.GatewayConfig)) (*server.Server, *gateway.Engine) {
	t.Helper()
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	cfg := &config.GatewayConfig{APIVersion: "proxy/v1", Listeners: []config.GatewayListenerConfig{{ID: "http", Address: "127.0.0.1:0", Protocol: "http1", Default: true}}, Routes: []config.GatewayRouteConfig{{ID: "root", Listener: "http", Match: config.GatewayRouteMatch{PathPrefix: "/"}, Action: config.GatewayRouteAction{Cluster: "api"}}}, Clusters: []config.GatewayClusterConfig{{ID: "api", Strategy: "round_robin", Endpoints: []config.GatewayEndpointConfig{{ID: "one", URL: backend.URL}}}}}
	for _, change := range configure {
		change(cfg)
	}
	engine, err := gateway.New(context.Background(), cfg, gateway.Defaults{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Enabled: false, Policy: ratelimit.Policy{Capacity: 100, RefillPerSecond: 100}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = limiter.Close() })
	options := testOptions(observability.NewMetrics())
	options.Gateway = engine
	options.ManagementAuthToken = ""
	for _, role := range []string{"viewer", "operator", "admin", "discovery", "metrics"} {
		options.Credentials = append(options.Credentials, server.Credential{Name: role, Role: role, Token: func() (string, error) { return role + "-secret", nil }})
	}
	result, err := server.NewServer(options, nil, limiter)
	require.NoError(t, err)
	return result, engine
}

func gatewayRequest(t *testing.T, instance *server.Server, role, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
	request.Header.Set("Authorization", "Bearer "+role+"-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Balancer-CSRF", "1")
	response := httptest.NewRecorder()
	instance.ManagementHandler().ServeHTTP(response, request)
	return response
}

func TestGatewayRoleBoundariesAndAudit(t *testing.T) {
	instance, engine := newGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	for _, role := range []string{"viewer", "operator", "admin"} {
		assert.Equal(t, 200, gatewayRequest(t, instance, role, "GET", "/api/v1/status", nil).Code)
	}
	for _, role := range []string{"discovery", "metrics"} {
		assert.Equal(t, 403, gatewayRequest(t, instance, role, "GET", "/api/v1/config", nil).Code)
	}
	assert.Equal(t, 403, gatewayRequest(t, instance, "viewer", "POST", "/api/v1/request", map[string]any{"path": "/"}).Code)
	assert.Equal(t, 403, gatewayRequest(t, instance, "operator", "PUT", "/api/v1/config", map[string]any{"config": engine.Config(), "expected_revision": engine.Status().Revision}).Code)
	assert.Equal(t, 403, gatewayRequest(t, instance, "operator", "PUT", "/api/v1/discovery/api", map[string]any{}).Code)
	assert.Equal(t, 403, gatewayRequest(t, instance, "viewer", "GET", "/api/dashboard/request", nil).Code)
	assert.Equal(t, 200, gatewayRequest(t, instance, "operator", "POST", "/api/v1/request", map[string]any{"path": "/"}).Code)
	audit := gatewayRequest(t, instance, "viewer", "GET", "/api/v1/audit", nil)
	assert.Contains(t, audit.Body.String(), `"subject":"operator"`)
	assert.NotContains(t, audit.Body.String(), "-secret")
}

func TestGatewayPlaygroundNeverForwardsManagementCredentials(t *testing.T) {
	var received http.Header
	instance, _ := newGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.Header().Set("Set-Cookie", "backend-cookie=secret")
		_, _ = io.WriteString(w, "ok")
	}))
	response := gatewayRequest(t, instance, "admin", "POST", "/api/v1/request", map[string]any{"path": "/", "method": "POST", "headers": map[string]string{"Authorization": "Bearer leaked", "Cookie": "sid=secret", "X-Balancer-CSRF": "1", "X-Forwarded-For": "1.2.3.4", "X-Lab": "accepted"}})
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Empty(t, received.Get("Authorization"))
	assert.Empty(t, received.Get("Cookie"))
	assert.Empty(t, received.Get("X-Balancer-CSRF"))
	assert.NotEqual(t, "1.2.3.4", received.Get("X-Forwarded-For"))
	assert.Equal(t, "accepted", received.Get("X-Lab"))
	assert.Contains(t, response.Body.String(), `"backend":"one"`)
	assert.NotContains(t, response.Body.String(), "backend-cookie")
}

func TestGatewayPlaygroundTruncatesStreamingBody(t *testing.T) {
	instance, _ := newGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 128<<10)) }))
	response := gatewayRequest(t, instance, "operator", "POST", "/api/v1/request", map[string]any{"path": "/"})
	require.Equal(t, 200, response.Code)
	var result struct {
		Body      string
		Truncated bool
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	assert.Len(t, result.Body, 64<<10)
	assert.True(t, result.Truncated)
}

func TestGatewayConfigCompareAndSwapAndRollback(t *testing.T) {
	instance, engine := newGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	before := engine.Status()
	next := engine.Config()
	next.Routes[0].Priority = 20
	assert.Equal(t, 409, gatewayRequest(t, instance, "admin", "PUT", "/api/v1/config", map[string]any{"config": next, "expected_revision": before.Revision + 1}).Code)
	assert.Equal(t, before.Hash, engine.Status().Hash)
	response := gatewayRequest(t, instance, "admin", "PUT", "/api/v1/config", map[string]any{"config": next, "expected_revision": before.Revision})
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.NotEqual(t, before.Hash, engine.Status().Hash)
	response = gatewayRequest(t, instance, "admin", "POST", "/api/v1/config/rollback", map[string]any{"expected_revision": engine.Status().Revision})
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Equal(t, before.Hash, engine.Status().Hash)
	next = engine.Config()
	next.Listeners[0].Address = ":12345"
	assert.Equal(t, 400, gatewayRequest(t, instance, "admin", "PUT", "/api/v1/config", map[string]any{"config": next, "expected_revision": engine.Status().Revision}).Code)
}

func TestManagementTokenFileRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("first"), 0600))
	t.Setenv("ROTATING_TOKEN_FILE", path)
	credentials := server.CredentialsFromConfig([]config.CredentialConfig{{Name: "reader", Role: "viewer", TokenEnv: "ROTATING_TOKEN"}})
	value, err := credentials[0].Token()
	require.NoError(t, err)
	assert.Equal(t, "first", value)
	require.NoError(t, os.WriteFile(path, []byte("second"), 0600))
	value, err = credentials[0].Token()
	require.NoError(t, err)
	assert.Equal(t, "second", value)
}

func startGatewayListeners(t *testing.T, instance *server.Server) string {
	t.Helper()
	public, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	management, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = instance.Serve(public, management) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = instance.Shutdown(ctx)
	})
	return public.Addr().String()
}

func TestGatewayH2CListenerAndStreaming(t *testing.T) {
	instance, _ := newGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: second\n\n")
	}), func(cfg *config.GatewayConfig) { cfg.Listeners[0].Protocol = "h2c" })
	address := startGatewayListeners(t, instance)
	transport := &http.Transport{Protocols: new(http.Protocols)}
	transport.Protocols.SetUnencryptedHTTP2(true)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("http://" + address + "/events")
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, 2, response.ProtoMajor)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Contains(t, string(data), "data: second")
}

func TestShutdownClosesHijackedConnectionsAtDeadline(t *testing.T) {
	instance, _ := newGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = buffer.Flush()
		_, _ = io.Copy(connection, connection)
	}))
	address := startGatewayListeners(t, instance)
	connection, err := net.Dial("tcp", address)
	require.NoError(t, err)
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(connection, "GET /socket HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	require.NoError(t, err)
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, status, "101")
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		if line == "\r\n" {
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, instance.Shutdown(ctx), context.DeadlineExceeded)
	_, err = reader.ReadByte()
	require.Error(t, err, "hijacked socket must not outlive shutdown budget")
}
