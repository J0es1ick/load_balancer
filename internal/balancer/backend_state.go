package balancer

import (
	"sync"
	"sync/atomic"
)

type backendState struct {
	stateMu            sync.Mutex
	pools              map[*BackendPool]struct{}
	healthOwner        *BackendPool
	configuredDisabled bool
	maxConcurrent      int64
	healthy            atomic.Bool
	enabled            atomic.Bool
	requests           atomic.Uint64
	passiveFailures    atomic.Int64
	consecutiveSuccess atomic.Int64
	consecutiveFailure atomic.Int64
	ejectedUntil       atomic.Int64
	healthySince       atomic.Int64
	inflight           atomic.Int64
	draining           atomic.Bool
}

type backendIdentity struct {
	scope string
	id    string
	url   string
}

type BackendStateRegistry struct {
	mu     sync.Mutex
	states map[backendIdentity]*registeredBackendState
}

type registeredBackendState struct {
	state *backendState
	refs  int
}

type backendStateReservation struct {
	key     backendIdentity
	backend *Backend
	shared  *registeredBackendState
	reused  bool
}

type BackendStateLease struct {
	registry *BackendStateRegistry
	pool     *BackendPool
	entries  []backendStateReservation
	active   bool
	closed   bool
}

func NewBackendStateRegistry() *BackendStateRegistry {
	return &BackendStateRegistry{states: make(map[backendIdentity]*registeredBackendState)}
}

func (state *backendState) invalidateLocked() {
	for pool := range state.pools {
		if until := state.ejectedUntil.Load(); until > 0 {
			pool.scheduleRecovery(until)
		}
		pool.stateVersion.Add(1)
	}
}

func (registry *BackendStateRegistry) Reserve(scope string, pool *BackendPool) *BackendStateLease {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	lease := &BackendStateLease{registry: registry, pool: pool}
	for _, backend := range pool.GetBackends() {
		key := backendIdentity{scope, backend.ID(), backend.URL.String()}
		shared, reused := registry.states[key]
		if !reused {
			shared = &registeredBackendState{state: backend.backendState}
			registry.states[key] = shared
		}
		shared.refs++
		lease.entries = append(lease.entries, backendStateReservation{key, backend, shared, reused})
	}
	return lease
}

func (lease *BackendStateLease) Reused(backend *Backend) bool {
	for _, entry := range lease.entries {
		if entry.backend == backend {
			return entry.reused
		}
	}
	return false
}

func (lease *BackendStateLease) Activate(refreshHealth bool) {
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if lease.closed || lease.active {
		return
	}
	lease.active = true
	for _, entry := range lease.entries {
		backend := entry.backend
		disabled := backend.configuredDisabled
		warmedHealthy := backend.IsHealthy()
		backend.backendState = entry.shared.state
		backend.stateMu.Lock()
		backend.pools[lease.pool] = struct{}{}
		backend.healthOwner = lease.pool
		if refreshHealth && entry.reused && !backend.IsEjected() {
			backend.setAliveLocked(warmedHealthy)
		}
		if backend.configuredDisabled != disabled {
			backend.configuredDisabled = disabled
			backend.enabled.Store(!disabled)
			backend.draining.Store(false)
		}
		backend.maxConcurrent = lease.pool.PassivePolicy().MaxConcurrentRequests
		backend.invalidateLocked()
		backend.stateMu.Unlock()
	}
}

func (lease *BackendStateLease) Close() {
	lease.registry.mu.Lock()
	defer lease.registry.mu.Unlock()
	if lease.closed {
		return
	}
	lease.closed = true
	for _, entry := range lease.entries {
		if lease.active {
			entry.shared.state.stateMu.Lock()
			delete(entry.shared.state.pools, lease.pool)
			entry.shared.state.stateMu.Unlock()
		}
		entry.shared.refs--
		if entry.shared.refs == 0 {
			delete(lease.registry.states, entry.key)
		}
	}
}
