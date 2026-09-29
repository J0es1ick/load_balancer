package balancer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stateTestPool(t *testing.T, registry *BackendStateRegistry, scope, address string, limit int64) (*BackendPool, *BackendStateLease) {
	t.Helper()
	pool, err := NewBackendPool([]BackendSpec{{ID: "one", URL: address}}, PassivePolicy{FailureThreshold: 1, Cooldown: time.Minute, MaxConcurrentRequests: limit, SlowStart: time.Minute, SlowStartMinimum: 10})
	require.NoError(t, err)
	pool.GetBackends()[0].SetAlive(true)
	lease := registry.Reserve(scope, pool)
	t.Cleanup(lease.Close)
	return pool, lease
}

func TestBackendStateSharedAcrossGenerations(t *testing.T) {
	registry := NewBackendStateRegistry()
	old, first := stateTestPool(t, registry, "api", "http://api:80", 2)
	first.Activate(false)
	backend := old.GetBackends()[0]
	backend.RecordRequest()
	require.True(t, backend.TryAcquire())
	next, second := stateTestPool(t, registry, "api", "http://api:80", 1)
	second.Activate(false)
	current := next.GetBackends()[0]
	assert.Same(t, backend.backendState, current.backendState)
	assert.Equal(t, uint64(1), current.Snapshot().Requests)
	assert.Equal(t, backend.healthySince.Load(), current.healthySince.Load())
	assert.False(t, backend.TryAcquire(), "old generations must enforce the current limit too")
	assert.False(t, current.TryAcquire())
	backend.Release()
	require.True(t, current.TryAcquire())
	current.Release()
	assert.True(t, old.Ready())
	assert.True(t, next.Ready())
	current.SetDraining(true)
	assert.False(t, old.Ready())
	assert.False(t, next.Ready())
	current.SetEnabled(true)
	assert.True(t, old.Ready())
	assert.True(t, next.Ready())
	backend.RecordPassiveFailure(old.PassivePolicy())
	assert.False(t, next.Ready(), "a late failure from a retired request must reach the current pool")
	assert.True(t, current.IsEjected())
	assert.False(t, old.Ready())
	first.Close()
	assert.Len(t, registry.states, 1)
	second.Close()
	assert.Empty(t, registry.states)
}

func TestBackendStateReservationDoesNotMutatePublishedPool(t *testing.T) {
	registry := NewBackendStateRegistry()
	old, first := stateTestPool(t, registry, "api", "http://api:80", 1)
	first.Activate(false)
	backend := old.GetBackends()[0]
	backend.SetDraining(true)
	candidate, second := stateTestPool(t, registry, "api", "http://api:80", 10)
	assert.True(t, second.Reused(candidate.GetBackends()[0]))
	candidate.GetBackends()[0].RecordPassiveFailure(candidate.PassivePolicy())
	second.Close()
	assert.True(t, backend.IsDraining())
	assert.True(t, backend.IsHealthy())
	assert.False(t, backend.IsEjected())
	assert.Equal(t, int64(1), backend.maxConcurrent)
	assert.Len(t, backend.pools, 1)
	first.Close()
	assert.Empty(t, registry.states)
}

func TestBackendStateReservationKeepsRetiredIdentityAlive(t *testing.T) {
	registry := NewBackendStateRegistry()
	old, first := stateTestPool(t, registry, "api", "http://api:80", 1)
	first.Activate(false)
	old.GetBackends()[0].RecordRequest()
	next, second := stateTestPool(t, registry, "api", "http://api:80", 1)
	first.Close()
	assert.Len(t, registry.states, 1)
	second.Activate(false)
	assert.Equal(t, uint64(1), next.GetBackends()[0].Snapshot().Requests)
	second.Close()
	assert.Empty(t, registry.states)
}

func TestBackendStateIdentityIncludesClusterAndURL(t *testing.T) {
	registry := NewBackendStateRegistry()
	one, first := stateTestPool(t, registry, "api", "http://one:80", 1)
	first.Activate(false)
	one.GetBackends()[0].SetDraining(true)
	for _, identity := range []struct{ scope, url string }{{"api", "http://two:80"}, {"other", "http://one:80"}} {
		pool, lease := stateTestPool(t, registry, identity.scope, identity.url, 1)
		lease.Activate(false)
		assert.True(t, pool.Ready())
		assert.NotSame(t, one.GetBackends()[0].backendState, pool.GetBackends()[0].backendState)
	}
}

func TestRetiredHealthCheckerCannotChangeCurrentState(t *testing.T) {
	registry := NewBackendStateRegistry()
	old, first := stateTestPool(t, registry, "api", "http://api:80", 1)
	first.Activate(false)
	next, second := stateTestPool(t, registry, "api", "http://api:80", 1)
	second.Activate(false)
	old.GetBackends()[0].RecordHealthResult(false, 1, 1)
	assert.True(t, next.Ready())
	next.GetBackends()[0].RecordHealthResult(false, 1, 1)
	assert.False(t, next.Ready())
	assert.False(t, old.Ready())
	old.GetBackends()[0].RecordHealthResult(true, 1, 1)
	assert.False(t, next.Ready())
}

func TestBackendStateCooldownSurvivesApplyAndRecoversEveryGeneration(t *testing.T) {
	registry := NewBackendStateRegistry()
	old, first := stateTestPool(t, registry, "api", "http://api:80", 1)
	first.Activate(false)
	backend := old.GetBackends()[0]
	backend.RecordPassiveFailure(old.PassivePolicy())
	next, second := stateTestPool(t, registry, "api", "http://api:80", 1)
	second.Activate(true)
	current := next.GetBackends()[0]
	assert.False(t, next.Ready(), "warmup must not bypass passive cooldown")
	assert.True(t, current.IsEjected())
	assert.Equal(t, backend.ejectedUntil.Load(), current.ejectedUntil.Load())
	backend.stateMu.Lock()
	backend.ejectedUntil.Store(time.Now().Add(-time.Second).UnixNano())
	backend.invalidateLocked()
	backend.stateMu.Unlock()
	assert.True(t, next.Ready())
	assert.True(t, old.Ready(), "recovery must invalidate every generation's availability cache")
}

func TestBackendStateRegistryReleasesRetiredGenerations(t *testing.T) {
	registry := NewBackendStateRegistry()
	pool, previous := stateTestPool(t, registry, "api", "http://api:80", 1)
	previous.Activate(false)
	state := pool.GetBackends()[0].backendState
	for range 100 {
		pool, current := stateTestPool(t, registry, "api", "http://api:80", 1)
		current.Activate(false)
		previous.Close()
		previous = current
		assert.Same(t, state, pool.GetBackends()[0].backendState)
		assert.Len(t, state.pools, 1)
		assert.Len(t, registry.states, 1)
	}
	previous.Close()
	assert.Empty(t, registry.states)
	assert.Empty(t, state.pools)
}
