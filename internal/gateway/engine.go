package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

var ErrClosed = errors.New("gateway is closed")
var ErrRevisionConflict = errors.New("gateway revision conflict")
var ErrDiscoverySourceChanged = errors.New("gateway discovery source changed")
var ErrStaleDiscoveryUpdate = errors.New("stale gateway discovery update")

type diagnosticsContextKey struct{}

func WithDiagnostics(ctx context.Context) context.Context {
	return context.WithValue(ctx, diagnosticsContextKey{}, true)
}

func diagnosticsEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(diagnosticsContextKey{}).(bool)
	return enabled
}

type engineStats struct {
	requests      atomic.Uint64
	routeNotFound atomic.Uint64
	redirects     atomic.Uint64
	rateLimited   atomic.Uint64
	bodyRejected  atomic.Uint64
	applySuccess  atomic.Uint64
	applyFailures atomic.Uint64
}

type Engine struct {
	context      context.Context
	cancel       context.CancelFunc
	defaults     Defaults
	observer     balancer.ProxyObserver
	current      atomic.Pointer[runtimeSnapshot]
	mu           sync.Mutex
	statusMu     sync.RWMutex
	closed       bool
	nextRevision uint64
	history      map[uint64]*config.GatewayConfig
	order        []uint64
	lastError    string
	stats        engineStats
}

func New(ctx context.Context, gatewayConfig *config.GatewayConfig, defaults Defaults, observer balancer.ProxyObserver) (*Engine, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lifecycle, cancel := context.WithCancel(ctx)
	engine := &Engine{context: lifecycle, cancel: cancel, defaults: defaults, observer: observer, nextRevision: 1, history: make(map[uint64]*config.GatewayConfig)}
	snapshot, err := buildSnapshot(lifecycle, ctx, gatewayConfig, defaults, observer, 1, 0)
	if err != nil {
		cancel()
		return nil, err
	}
	engine.current.Store(snapshot)
	engine.remember(snapshot)
	engine.stats.applySuccess.Add(1)
	snapshot.start()
	return engine, nil
}

func (engine *Engine) Handler(listenerID string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		engine.ServeHTTP(writer, withListener(request, listenerID))
	})
}

func (engine *Engine) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := validateIncomingRequest(request); err != nil {
		http.Error(writer, "Bad request", http.StatusBadRequest)
		return
	}
	snapshot := engine.acquireSnapshot()
	if snapshot == nil {
		http.Error(writer, "Service not available", http.StatusServiceUnavailable)
		return
	}
	defer snapshot.release()
	engine.stats.requests.Add(1)
	listenerID := listenerFromRequest(request)
	listener, err := validateListener(snapshot, listenerID)
	if err != nil {
		engine.stats.routeNotFound.Add(1)
		http.NotFound(writer, request)
		return
	}
	if listenerID == "" {
		listenerID = listener.ID
	}
	var selected *compiledRoute
	for _, route := range snapshot.routes {
		if route.matches(listenerID, listener, request) {
			selected = route
			break
		}
	}
	if selected == nil {
		engine.stats.routeNotFound.Add(1)
		http.NotFound(writer, request)
		return
	}
	diagnostics := diagnosticsEnabled(request.Context())
	responseWriter := &headerPolicyWriter{ResponseWriter: writer, policy: selected.config.ResponseHeaders, diagnostics: diagnostics}
	if diagnostics {
		responseWriter.Header().Set("X-Balancer-Route", selected.config.ID)
	}
	if selected.redirect(responseWriter, request) {
		engine.stats.redirects.Add(1)
		return
	}
	clusterID := selected.clusterID()
	cluster := snapshot.clusters[clusterID]
	if cluster == nil {
		http.Error(responseWriter, "Service not available", http.StatusServiceUnavailable)
		return
	}
	if diagnostics {
		responseWriter.Header().Set("X-Balancer-Cluster", clusterID)
	}
	prepared, cancel, ok, rejection := selected.prepareRequest(responseWriter, request, cluster.loadBalancer.RetryPolicy())
	defer cancel()
	if !ok {
		if rejection == "rate_limit" {
			engine.stats.rateLimited.Add(1)
		}
		if rejection == "body" {
			engine.stats.bodyRejected.Add(1)
		}
		return
	}
	cluster.loadBalancer.ServeHTTP(responseWriter, prepared)
	if responseWriter.status == http.StatusRequestEntityTooLarge {
		engine.stats.bodyRejected.Add(1)
	}
}

func (engine *Engine) Validate(ctx context.Context, gatewayConfig *config.GatewayConfig) error {
	_ = ctx
	_, err := normalizeConfig(gatewayConfig, engine.defaults)
	return err
}

func (engine *Engine) Apply(ctx context.Context, gatewayConfig *config.GatewayConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.applyLocked(ctx, gatewayConfig)
}

func (engine *Engine) ApplyIfRevision(ctx context.Context, gatewayConfig *config.GatewayConfig, expectedRevision uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return ErrClosed
	}
	current := engine.current.Load()
	if current == nil || expectedRevision == 0 || current.revision != expectedRevision {
		actual := uint64(0)
		if current != nil {
			actual = current.revision
		}
		return fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, expectedRevision, actual)
	}
	return engine.applyLocked(ctx, gatewayConfig)
}

func (engine *Engine) applyLocked(ctx context.Context, gatewayConfig *config.GatewayConfig) error {
	if engine.closed {
		return ErrClosed
	}
	current := engine.current.Load()
	previous := uint64(0)
	if current != nil {
		previous = current.revision
	}
	revision := engine.nextRevision + 1
	snapshot, err := buildSnapshot(engine.context, ctx, gatewayConfig, engine.defaults, engine.observer, revision, previous)
	if err != nil {
		engine.setLastError(err.Error())
		engine.stats.applyFailures.Add(1)
		return err
	}
	engine.nextRevision = revision
	engine.setLastError("")
	old := engine.current.Swap(snapshot)
	engine.remember(snapshot)
	engine.stats.applySuccess.Add(1)
	snapshot.start()
	if old != nil {
		old.retire()
	}
	return nil
}

func (engine *Engine) Rollback(ctx context.Context, revision uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return ErrClosed
	}
	return engine.rollbackLocked(ctx, revision)
}

func (engine *Engine) RollbackIfRevision(ctx context.Context, revision, expectedRevision uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return ErrClosed
	}
	current := engine.current.Load()
	if current == nil || expectedRevision == 0 || current.revision != expectedRevision {
		actual := uint64(0)
		if current != nil {
			actual = current.revision
		}
		return fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, expectedRevision, actual)
	}
	return engine.rollbackLocked(ctx, revision)
}

func (engine *Engine) rollbackLocked(ctx context.Context, revision uint64) error {
	value, exists := engine.history[revision]
	if !exists {
		return fmt.Errorf("gateway revision %d is not available", revision)
	}
	copy, err := cloneConfig(value)
	if err != nil {
		return err
	}
	return engine.applyLocked(ctx, copy)
}

func (engine *Engine) Config() *config.GatewayConfig {
	snapshot := engine.acquireSnapshot()
	if snapshot == nil {
		return nil
	}
	defer snapshot.release()
	copy, err := cloneConfig(snapshot.config)
	if err != nil {
		return nil
	}
	return copy
}

func (engine *Engine) Status() Status {
	snapshot := engine.acquireSnapshot()
	engine.statusMu.RLock()
	lastError := engine.lastError
	engine.statusMu.RUnlock()
	status := Status{LastError: lastError, Stats: engine.statsSnapshot()}
	if snapshot == nil {
		return status
	}
	defer snapshot.release()
	status.Revision = snapshot.revision
	status.PreviousRevision = snapshot.previousRevision
	status.Hash = snapshot.hash
	status.AppliedAt = snapshot.appliedAt
	for _, listener := range snapshot.config.Listeners {
		status.Listeners = append(status.Listeners, ListenerStatus{ID: listener.ID, Address: listener.Address, Protocol: listener.Protocol, TLS: listener.TLS.CertFile != ""})
	}
	for _, route := range snapshot.config.Routes {
		status.Routes = append(status.Routes, RouteStatus{ID: route.ID, Listener: route.Listener, Priority: route.Priority})
	}
	for _, configured := range snapshot.config.Clusters {
		cluster := snapshot.clusters[configured.ID]
		if cluster == nil {
			continue
		}
		policy := cluster.loadBalancer.RetryPolicy()
		discovery := cluster.discoveryStatus()
		if discovery.Type == "" {
			discovery.Type = configured.Discovery.Type
		}
		if !discovery.LastUpdate.IsZero() && configured.Discovery.StaleAfter.Duration() > 0 && time.Since(discovery.LastUpdate) > configured.Discovery.StaleAfter.Duration() {
			discovery.Stale = true
		}
		status.Clusters = append(status.Clusters, ClusterStatus{ID: configured.ID, Strategy: configured.Strategy, Endpoints: cluster.pool.Snapshot(), Discovery: discovery, TLS: TLSStatus{Enabled: clusterUsesTLS(configured), ServerName: configured.TLS.ServerName}, Retry: RetryStatus{MaxAttempts: policy.MaxAttempts, PerTryTimeout: policy.PerTryTimeout.String(), Methods: append([]string(nil), policy.Methods...), Statuses: append([]int(nil), policy.Statuses...), Budget: cluster.loadBalancer.RetryBudget()}})
	}
	return status
}

func clusterUsesTLS(cluster config.GatewayClusterConfig) bool {
	if cluster.TLS.Enabled {
		return true
	}
	for _, endpoint := range cluster.Endpoints {
		if len(endpoint.URL) >= 8 && endpoint.URL[:8] == "https://" {
			return true
		}
	}
	return false
}

func (engine *Engine) Ready() bool {
	snapshot := engine.acquireSnapshot()
	if snapshot == nil {
		return false
	}
	defer snapshot.release()
	required := make(map[string]struct{})
	for _, route := range snapshot.config.Routes {
		if route.Action.Redirect != nil {
			continue
		}
		if route.Action.Cluster != "" {
			required[route.Action.Cluster] = struct{}{}
		}
		for _, target := range route.Action.WeightedClusters {
			required[target.Cluster] = struct{}{}
		}
	}
	for clusterID := range required {
		cluster := snapshot.clusters[clusterID]
		if cluster == nil || !cluster.pool.Ready() {
			return false
		}
	}
	return true
}

func (engine *Engine) SetEndpoint(clusterID, endpointID string, enabled bool) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	snapshot := engine.current.Load()
	if snapshot == nil {
		return ErrClosed
	}
	cluster, exists := snapshot.clusters[clusterID]
	if !exists {
		return fmt.Errorf("unknown gateway cluster %q", clusterID)
	}
	if !cluster.pool.SetBackendEnabled(endpointID, enabled) {
		return fmt.Errorf("unknown endpoint %q in cluster %q", endpointID, clusterID)
	}
	return nil
}

func (engine *Engine) DrainEndpoint(clusterID, endpointID string) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	snapshot := engine.current.Load()
	if snapshot == nil {
		return ErrClosed
	}
	cluster, exists := snapshot.clusters[clusterID]
	if !exists {
		return fmt.Errorf("unknown gateway cluster %q", clusterID)
	}
	if !cluster.pool.DrainBackend(endpointID) {
		return fmt.Errorf("unknown endpoint %q in cluster %q", endpointID, clusterID)
	}
	return nil
}

func (engine *Engine) UpdateEndpoints(ctx context.Context, clusterID string, endpoints []config.GatewayEndpointConfig, discovery DiscoveryStatus) error {
	if ctx == nil {
		ctx = context.Background()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.updateEndpointsLocked(ctx, clusterID, endpoints, discovery, nil)
}

func (engine *Engine) UpdateEndpointsForSource(ctx context.Context, clusterID string, endpoints []config.GatewayEndpointConfig, discovery DiscoveryStatus, expected config.GatewayDiscoveryConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.updateEndpointsLocked(ctx, clusterID, endpoints, discovery, &expected)
}

func (engine *Engine) updateEndpointsLocked(ctx context.Context, clusterID string, endpoints []config.GatewayEndpointConfig, discovery DiscoveryStatus, expected *config.GatewayDiscoveryConfig) error {
	if engine.closed {
		return ErrClosed
	}
	current := engine.current.Load()
	if current == nil {
		return ErrClosed
	}
	copy, err := cloneConfig(current.config)
	if err != nil {
		return err
	}
	found := false
	unchanged := false
	for index := range copy.Clusters {
		if copy.Clusters[index].ID == clusterID {
			if expected != nil && copy.Clusters[index].Discovery != *expected {
				return fmt.Errorf("%w for cluster %q", ErrDiscoverySourceChanged, clusterID)
			}
			if discovery.Type == "" {
				discovery.Type = copy.Clusters[index].Discovery.Type
			}
			if discovery.Type != copy.Clusters[index].Discovery.Type {
				return fmt.Errorf("%w for cluster %q: got %q, configured %q", ErrDiscoverySourceChanged, clusterID, discovery.Type, copy.Clusters[index].Discovery.Type)
			}
			if cluster := current.clusters[clusterID]; cluster != nil {
				last := cluster.discoveryStatus().LastUpdate
				if !discovery.LastUpdate.IsZero() && !last.IsZero() && discovery.LastUpdate.Before(last) {
					return fmt.Errorf("%w for cluster %q", ErrStaleDiscoveryUpdate, clusterID)
				}
			}
			normalizedEndpoints := normalizeEndpoints(endpoints)
			unchanged = endpointSetsEqual(copy.Clusters[index].Endpoints, normalizedEndpoints)
			copy.Clusters[index].Endpoints = normalizedEndpoints
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown gateway cluster %q", clusterID)
	}
	if err := copy.Validate(); err != nil {
		return err
	}
	if unchanged {
		if cluster := current.clusters[clusterID]; cluster != nil {
			cluster.setDiscovery(discovery)
		}
		return nil
	}
	if err := engine.applyLocked(ctx, copy); err != nil {
		return err
	}
	updated := engine.current.Load()
	if updated != nil {
		if cluster := updated.clusters[clusterID]; cluster != nil {
			cluster.setDiscovery(discovery)
		}
	}
	return nil
}

func normalizeEndpoints(values []config.GatewayEndpointConfig) []config.GatewayEndpointConfig {
	result := append([]config.GatewayEndpointConfig(nil), values...)
	for index := range result {
		if result[index].Weight == 0 {
			result[index].Weight = 1
		}
	}
	return result
}

func endpointSetsEqual(left, right []config.GatewayEndpointConfig) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]config.GatewayEndpointConfig, len(left))
	for _, endpoint := range normalizeEndpoints(left) {
		values[endpoint.ID] = endpoint
	}
	for _, endpoint := range normalizeEndpoints(right) {
		if values[endpoint.ID] != endpoint {
			return false
		}
	}
	return true
}

func (engine *Engine) UpdateDiscoveryStatus(clusterID string, status DiscoveryStatus) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	snapshot := engine.current.Load()
	if snapshot == nil {
		return ErrClosed
	}
	cluster, exists := snapshot.clusters[clusterID]
	if !exists {
		return fmt.Errorf("unknown gateway cluster %q", clusterID)
	}
	cluster.setDiscovery(status)
	return nil
}

func (engine *Engine) MarkDiscoveryError(clusterID string, discoveryError error, stale bool) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.markDiscoveryErrorLocked(clusterID, discoveryError, stale, nil)
}

func (engine *Engine) MarkDiscoveryErrorForSource(clusterID string, discoveryError error, stale bool, expected config.GatewayDiscoveryConfig) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.markDiscoveryErrorLocked(clusterID, discoveryError, stale, &expected)
}

func (engine *Engine) markDiscoveryErrorLocked(clusterID string, discoveryError error, stale bool, expected *config.GatewayDiscoveryConfig) error {
	snapshot := engine.current.Load()
	if snapshot == nil {
		return ErrClosed
	}
	cluster, exists := snapshot.clusters[clusterID]
	if !exists {
		return fmt.Errorf("unknown gateway cluster %q", clusterID)
	}
	if expected != nil && cluster.config.Discovery != *expected {
		return fmt.Errorf("%w for cluster %q", ErrDiscoverySourceChanged, clusterID)
	}
	status := cluster.discoveryStatus()
	status.Stale = stale
	if discoveryError != nil {
		status.Error = discoveryError.Error()
	} else {
		status.Error = ""
	}
	cluster.setDiscovery(status)
	return nil
}

func (engine *Engine) Close() error {
	engine.mu.Lock()
	if engine.closed {
		engine.mu.Unlock()
		return nil
	}
	engine.closed = true
	engine.cancel()
	snapshot := engine.current.Swap(nil)
	engine.mu.Unlock()
	if snapshot != nil {
		snapshot.retire()
	}
	return nil
}

func (engine *Engine) acquireSnapshot() *runtimeSnapshot {
	for {
		snapshot := engine.current.Load()
		if snapshot == nil {
			return nil
		}
		if snapshot.acquire() {
			return snapshot
		}
	}
}

func (engine *Engine) remember(snapshot *runtimeSnapshot) {
	copy, err := cloneConfig(snapshot.config)
	if err != nil {
		return
	}
	engine.history[snapshot.revision] = copy
	engine.order = append(engine.order, snapshot.revision)
	limit := snapshot.config.HistoryLimit
	if engine.defaults.HistoryLimit > 0 {
		limit = min(limit, engine.defaults.HistoryLimit)
	}
	for len(engine.order) > limit {
		oldest := engine.order[0]
		engine.order = engine.order[1:]
		delete(engine.history, oldest)
	}
}

func (engine *Engine) statsSnapshot() Stats {
	return Stats{Requests: engine.stats.requests.Load(), RouteNotFound: engine.stats.routeNotFound.Load(), Redirects: engine.stats.redirects.Load(), RateLimited: engine.stats.rateLimited.Load(), BodyRejected: engine.stats.bodyRejected.Load(), ApplySuccess: engine.stats.applySuccess.Load(), ApplyFailures: engine.stats.applyFailures.Load()}
}

func (engine *Engine) setLastError(value string) {
	engine.statusMu.Lock()
	engine.lastError = value
	engine.statusMu.Unlock()
}
