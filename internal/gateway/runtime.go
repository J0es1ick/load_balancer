package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

type runtimeCluster struct {
	config       config.GatewayClusterConfig
	pool         *balancer.BackendPool
	loadBalancer *balancer.LoadBalancer
	health       *balancer.HealthChecker
	transport    *http.Transport
	discoveryMu  sync.RWMutex
	discovery    DiscoveryStatus
}

type runtimeSnapshot struct {
	config           *config.GatewayConfig
	revision         uint64
	previousRevision uint64
	hash             string
	appliedAt        time.Time
	listeners        map[string]config.GatewayListenerConfig
	defaultListener  string
	routes           []*compiledRoute
	clusters         map[string]*runtimeCluster
	context          context.Context
	cancel           context.CancelFunc
	refs             atomic.Int64
	retired          atomic.Bool
	closed           atomic.Bool
	closeOnce        sync.Once
}

func buildSnapshot(lifecycle, operation context.Context, gatewayConfig *config.GatewayConfig, defaults Defaults, observer balancer.ProxyObserver, revision, previous uint64) (*runtimeSnapshot, error) {
	normalized, err := normalizeConfig(gatewayConfig, defaults)
	if err != nil {
		return nil, err
	}
	hash, err := configHash(normalized)
	if err != nil {
		return nil, fmt.Errorf("hash gateway config: %w", err)
	}
	ctx, cancel := context.WithCancel(lifecycle)
	snapshot := &runtimeSnapshot{config: normalized, revision: revision, previousRevision: previous, hash: hash, appliedAt: time.Now().UTC(), listeners: make(map[string]config.GatewayListenerConfig, len(normalized.Listeners)), clusters: make(map[string]*runtimeCluster, len(normalized.Clusters)), context: ctx, cancel: cancel}
	fail := func(err error) (*runtimeSnapshot, error) { snapshot.close(); return nil, err }
	for _, listener := range normalized.Listeners {
		snapshot.listeners[listener.ID] = listener
		if listener.Default {
			snapshot.defaultListener = listener.ID
		}
	}
	if snapshot.defaultListener == "" && len(normalized.Listeners) > 0 {
		snapshot.defaultListener = normalized.Listeners[0].ID
	}
	warmupTimeout := defaults.WarmupTimeout
	if warmupTimeout <= 0 {
		warmupTimeout = 30 * time.Second
	}
	for _, clusterConfig := range normalized.Clusters {
		cluster, clusterErr := buildCluster(operation, clusterConfig, defaults, observer, warmupTimeout)
		if clusterErr != nil {
			return fail(fmt.Errorf("build cluster %q: %w", clusterConfig.ID, clusterErr))
		}
		snapshot.clusters[clusterConfig.ID] = cluster
	}
	routes, err := compileRoutes(normalized.Routes)
	if err != nil {
		return fail(err)
	}
	snapshot.routes = routes
	return snapshot, nil
}

func buildCluster(operation context.Context, value config.GatewayClusterConfig, defaults Defaults, observer balancer.ProxyObserver, warmupTimeout time.Duration) (*runtimeCluster, error) {
	specs := make([]balancer.BackendSpec, 0, len(value.Endpoints))
	for _, endpoint := range value.Endpoints {
		specs = append(specs, balancer.BackendSpec{ID: endpoint.ID, URL: endpoint.URL, Weight: endpoint.Weight, Disabled: endpoint.Disabled})
	}
	maxConcurrent := int64(value.Transport.MaxConcurrentRequests)
	if maxConcurrent < 1 {
		maxConcurrent = 512
	}
	health := healthSettings(value.Health, maxConcurrent)
	pool, err := balancer.NewBackendPool(specs, balancer.PassivePolicy{FailureThreshold: int64(max(1, health.FailureThreshold)), Cooldown: health.Cooldown, MaxConcurrentRequests: maxConcurrent, SlowStart: health.SlowStart, SlowStartMinimum: health.SlowStartMinimum})
	if err != nil {
		return nil, err
	}
	transport, err := clusterTransport(value)
	if err != nil {
		return nil, err
	}
	var roundTripper http.RoundTripper = transport
	if defaults.WrapTransport != nil {
		roundTripper = defaults.WrapTransport(roundTripper)
	}
	health.Transport = roundTripper
	strategy, err := clusterStrategy(value)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	loadBalancer := balancer.NewLoadBalancer(pool, strategy, balancer.LoadBalancerOptions{Transport: roundTripper, Retry: retryPolicy(value.Retry), Observer: observer})
	cluster := &runtimeCluster{config: value, pool: pool, loadBalancer: loadBalancer, transport: transport, discovery: DiscoveryStatus{Type: value.Discovery.Type}}
	if !value.Health.Enabled {
		for _, backend := range pool.GetBackends() {
			backend.SetAlive(true)
		}
		return cluster, nil
	}
	checker, err := balancer.NewHealthChecker(pool, health)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	cluster.health = checker
	if len(specs) > 0 {
		warmContext, cancel := context.WithTimeout(operation, warmupTimeout)
		defer cancel()
		for range health.SuccessThreshold {
			checker.Check(warmContext)
		}
		if warmContext.Err() != nil {
			transport.CloseIdleConnections()
			return nil, fmt.Errorf("health warmup: %w", warmContext.Err())
		}
		enabled := 0
		for _, backend := range pool.GetBackends() {
			if backend.IsEnabled() {
				enabled++
			}
		}
		if enabled > 0 && !pool.Ready() {
			transport.CloseIdleConnections()
			return nil, fmt.Errorf("no enabled endpoint passed warmup")
		}
	}
	return cluster, nil
}

func clusterTransport(value config.GatewayClusterConfig) (*http.Transport, error) {
	tlsConfig, err := clientTLSConfig(value.TLS)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		DialContext:            (&net.Dialer{Timeout: value.Transport.DialTimeout.Duration(), KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:    value.Transport.TLSHandshakeTimeout.Duration(),
		ResponseHeaderTimeout:  value.Transport.ResponseHeaderTimeout.Duration(),
		ExpectContinueTimeout:  value.Transport.ExpectContinueTimeout.Duration(),
		IdleConnTimeout:        value.Transport.IdleConnTimeout.Duration(),
		MaxIdleConns:           value.Transport.MaxIdleConns,
		MaxIdleConnsPerHost:    value.Transport.MaxIdleConnsPerHost,
		MaxConnsPerHost:        value.Transport.MaxConnsPerHost,
		MaxResponseHeaderBytes: value.Transport.MaxResponseHeaderBytes,
		TLSClientConfig:        tlsConfig,
	}
	protocols := new(http.Protocols)
	switch value.Transport.Protocol {
	case "auto":
		protocols.SetHTTP1(true)
		protocols.SetHTTP2(true)
	case "http1":
		protocols.SetHTTP1(true)
	case "http2":
		protocols.SetHTTP2(true)
	case "h2c":
		protocols.SetUnencryptedHTTP2(true)
	default:
		return nil, fmt.Errorf("unsupported transport protocol %q", value.Transport.Protocol)
	}
	transport.Protocols = protocols
	return transport, nil
}

func clientTLSConfig(value config.ClientTLSConfig) (*tls.Config, error) {
	result := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: value.ServerName}
	if value.CAFile != "" {
		data, err := os.ReadFile(value.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read upstream CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("upstream CA file contains no certificates")
		}
		result.RootCAs = roots
	}
	if value.ClientCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(value.ClientCertFile, value.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load upstream client certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}

func clusterStrategy(value config.GatewayClusterConfig) (balancer.Strategy, error) {
	switch value.Strategy {
	case "round_robin":
		return balancer.NewRoundRobinStrategy(), nil
	case "weighted_round_robin":
		return balancer.NewWeightedRoundRobinStrategy(), nil
	case "least_request":
		return balancer.NewLeastRequestStrategy(), nil
	case "rendezvous":
		return balancer.NewRendezvousStrategy(rendezvousKey(value.HashKey)), nil
	default:
		return nil, fmt.Errorf("unsupported strategy %q", value.Strategy)
	}
}

func rendezvousKey(expression string) func(*http.Request) string {
	parts := strings.SplitN(expression, ":", 2)
	return func(request *http.Request) string {
		switch parts[0] {
		case "client_ip":
			return balancer.VerifiedClientIP(request.Context())
		case "header":
			if len(parts) == 2 {
				return request.Header.Get(parts[1])
			}
		case "cookie":
			if len(parts) == 2 {
				if cookie, err := request.Cookie(parts[1]); err == nil {
					return cookie.Value
				}
			}
		case "host":
			return request.Host
		case "path":
			return request.URL.EscapedPath()
		}
		return request.Header.Get("X-Request-ID")
	}
}

func (snapshot *runtimeSnapshot) start() {
	for _, cluster := range snapshot.clusters {
		if cluster.health != nil {
			go cluster.health.Start(snapshot.context)
		}
	}
}

func (snapshot *runtimeSnapshot) acquire() bool {
	snapshot.refs.Add(1)
	if snapshot.retired.Load() {
		snapshot.release()
		return false
	}
	return true
}

func (snapshot *runtimeSnapshot) release() {
	if snapshot.refs.Add(-1) == 0 && snapshot.retired.Load() {
		snapshot.close()
	}
}

func (snapshot *runtimeSnapshot) retire() {
	snapshot.retired.Store(true)
	if snapshot.cancel != nil {
		snapshot.cancel()
	}
	if snapshot.refs.Load() == 0 {
		snapshot.close()
	}
}

func (snapshot *runtimeSnapshot) close() {
	snapshot.closeOnce.Do(func() {
		snapshot.closed.Store(true)
		if snapshot.cancel != nil {
			snapshot.cancel()
		}
		for _, cluster := range snapshot.clusters {
			cluster.transport.CloseIdleConnections()
		}
	})
}

func (cluster *runtimeCluster) discoveryStatus() DiscoveryStatus {
	cluster.discoveryMu.RLock()
	defer cluster.discoveryMu.RUnlock()
	return cluster.discovery
}

func (cluster *runtimeCluster) setDiscovery(status DiscoveryStatus) {
	cluster.discoveryMu.Lock()
	cluster.discovery = status
	cluster.discoveryMu.Unlock()
}
