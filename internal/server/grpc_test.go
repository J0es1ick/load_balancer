package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestRealGRPCUnaryAndStreamingThroughH2CGateway(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	upstream := grpc.NewServer()
	healthService := health.NewServer()
	healthService.SetServingStatus("ready", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(upstream, healthService)
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Stop()
	cfg := &config.GatewayConfig{APIVersion: "proxy/v1", Listeners: []config.GatewayListenerConfig{{ID: "grpc", Address: "127.0.0.1:0", Protocol: "h2c", Default: true}}, Routes: []config.GatewayRouteConfig{{ID: "grpc-health", Match: config.GatewayRouteMatch{PathPrefix: "/"}, Action: config.GatewayRouteAction{Cluster: "health"}}}, Clusters: []config.GatewayClusterConfig{{ID: "health", Endpoints: []config.GatewayEndpointConfig{{ID: "one", URL: "http://" + listener.Addr().String()}}, Transport: config.GatewayTransportConfig{Protocol: "h2c"}}}}
	engine, err := gateway.New(context.Background(), cfg, gateway.Defaults{}, nil)
	require.NoError(t, err)
	defer engine.Close()
	limiter, err := ratelimit.NewTokenBucketLimiter(ratelimit.RuntimeSettings{Policy: ratelimit.Policy{Capacity: 10, RefillPerSecond: 10}, FailureMode: "fail-open", OperationTimeout: time.Second}, ratelimit.NewLocalStore(1), nil)
	require.NoError(t, err)
	defer limiter.Close()
	options := testOptions(observability.NewMetrics())
	options.Gateway = engine
	instance, err := server.NewServer(options, nil, limiter)
	require.NoError(t, err)
	address := startGatewayListeners(t, instance)
	client, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer client.Close()
	healthClient := grpc_health_v1.NewHealthClient(client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "ready"})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.Status)
	_, err = healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "unknown"})
	require.Equal(t, codes.NotFound, status.Code(err), "grpc-status trailer must survive proxying")
	stream, err := healthClient.Watch(ctx, &grpc_health_v1.HealthCheckRequest{Service: "ready"})
	require.NoError(t, err)
	response, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.Status)
	healthService.SetServingStatus("ready", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	response, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, response.Status)
}
