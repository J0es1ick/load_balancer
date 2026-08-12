package balancer_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendPoolUsesExplicitStableIDs(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{
		{ID: "api-a", URL: "http://localhost:8081"},
		{ID: "api-b", URL: "http://localhost:8082"},
	})
	require.NoError(t, err)
	require.Len(t, pool.GetBackends(), 2)
	assert.Equal(t, "api-a", pool.GetBackends()[0].ID())
	pool.GetBackends()[0].SetAlive(true)
	assert.True(t, pool.Ready())
	assert.True(t, pool.SetBackendEnabled("api-a", false))
	assert.False(t, pool.Ready())
}

func TestBackendPoolSupportsDisabledReserveAndActiveCount(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{
		{ID: "one", URL: "http://one:80"},
		{ID: "two", URL: "http://two:80"},
		{ID: "three", URL: "http://three:80", Disabled: true},
	})
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	require.Len(t, pool.AvailableBackends(), 2)
	assert.False(t, pool.GetBackends()[2].IsEnabled())

	assert.True(t, pool.SetActiveCount(3))
	require.Len(t, pool.AvailableBackends(), 3)
	assert.True(t, pool.GetBackends()[2].IsEnabled())

	assert.True(t, pool.SetActiveCount(1))
	require.Len(t, pool.AvailableBackends(), 1)
	assert.Equal(t, "one", pool.AvailableBackends()[0].ID())
	assert.False(t, pool.SetActiveCount(0))
	assert.False(t, pool.SetActiveCount(4))
}

func TestBackendPoolReloadAppliesDeclarativeDisabledState(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]
	backend.SetAlive(true)
	backend.SetDraining(true)

	require.NoError(t, pool.ReplaceBackends([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}))
	require.Same(t, backend, pool.GetBackends()[0], "stable backend identity should preserve counters and health")
	assert.True(t, backend.IsEnabled())
	assert.False(t, backend.IsDraining(), "declarative reload supersedes an ephemeral drain")
	assert.True(t, backend.IsAlive())

	require.NoError(t, pool.ReplaceBackends([]balancer.BackendSpec{{ID: "api", URL: "http://api:80", Disabled: true}}))
	require.Same(t, backend, pool.GetBackends()[0])
	assert.False(t, backend.IsEnabled())
	assert.False(t, backend.IsDraining())
	assert.False(t, backend.IsAlive())
}

func TestBackendPoolPublishesConcurrentHealthChangesConsistently(t *testing.T) {
	specs := make([]balancer.BackendSpec, 64)
	for index := range specs {
		specs[index] = balancer.BackendSpec{ID: fmt.Sprintf("backend-%d", index), URL: fmt.Sprintf("http://127.0.0.1:%d", 10_000+index)}
	}
	pool, err := balancer.NewBackendPool(specs)
	require.NoError(t, err)
	for _, backend := range pool.GetBackends() {
		backend.SetAlive(true)
	}
	require.Len(t, pool.AvailableBackends(), len(specs))

	var waitGroup sync.WaitGroup
	for _, backend := range pool.GetBackends() {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			backend.SetAlive(false)
		}()
	}
	waitGroup.Wait()
	assert.Empty(t, pool.AvailableBackends())
}

func TestBackendPoolRejectsDuplicateIDAndURL(t *testing.T) {
	_, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "same", URL: "http://one:80"}, {ID: "same", URL: "http://two:80"}})
	assert.ErrorContains(t, err, "duplicate backend ID")
	_, err = balancer.NewBackendPool([]balancer.BackendSpec{{ID: "one", URL: "http://one:80"}, {ID: "two", URL: "http://one:80"}})
	assert.ErrorContains(t, err, "duplicate backend URL")
}

func TestPassiveFailureEjectsBackend(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}, balancer.PassivePolicy{FailureThreshold: 2, Cooldown: time.Minute})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]
	backend.SetAlive(true)
	backend.RecordPassiveFailure(pool.PassivePolicy())
	assert.True(t, backend.IsAlive())
	backend.RecordPassiveFailure(pool.PassivePolicy())
	assert.False(t, backend.IsAlive())
	assert.True(t, backend.IsEjected())
}

func TestPassiveCooldownCannotBeBypassedByActiveHealth(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}, balancer.PassivePolicy{FailureThreshold: 1, Cooldown: 30 * time.Millisecond, MaxConcurrentRequests: 8, SlowStartMinimum: 100})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]
	backend.SetAlive(true)
	backend.RecordPassiveFailure(pool.PassivePolicy())
	backend.RecordHealthResult(true, 1, 1)
	assert.False(t, backend.IsAlive())
	time.Sleep(40 * time.Millisecond)
	backend.RecordHealthResult(true, 1, 1)
	assert.True(t, backend.IsAlive())
}

func TestConcurrentPassiveFailureCannotBeClearedByActiveHealth(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}, balancer.PassivePolicy{FailureThreshold: 1, Cooldown: time.Minute, MaxConcurrentRequests: 8, SlowStartMinimum: 100})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]

	for range 1_000 {
		backend.SetAlive(true)
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			<-start
			backend.RecordHealthResult(true, 1, 1)
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			backend.RecordPassiveFailure(pool.PassivePolicy())
		}()
		close(start)
		waitGroup.Wait()
		require.True(t, backend.IsEjected(), "a concurrent active check must not clear passive ejection")
		require.False(t, backend.IsAlive())
	}
}

func TestBackendDrainStopsNewTrafficAndPreservesState(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}, balancer.PassivePolicy{FailureThreshold: 2, Cooldown: time.Second, MaxConcurrentRequests: 8, SlowStartMinimum: 100})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]
	backend.SetAlive(true)
	require.True(t, backend.TryAcquire())
	assert.True(t, pool.DrainBackend("api"))
	assert.False(t, backend.IsAlive())
	assert.True(t, backend.IsDraining())
	assert.Equal(t, int64(1), backend.Inflight())
	assert.False(t, backend.TryAcquire(), "drain must reject every new acquisition")
	assert.Equal(t, int64(1), backend.Inflight(), "a rejected acquisition must be rolled back")
	backend.Release()
	backend.SetEnabled(true)
	assert.True(t, backend.IsAlive())
	assert.False(t, backend.IsDraining())
}

func TestConcurrentDrainNeverLeavesAnAcquisitionAfterDrain(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}, balancer.PassivePolicy{FailureThreshold: 2, Cooldown: time.Second, MaxConcurrentRequests: 8, SlowStartMinimum: 100})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]
	backend.SetAlive(true)

	for range 1_000 {
		backend.SetEnabled(true)
		start := make(chan struct{})
		acquired := make(chan bool, 1)
		drained := make(chan struct{})
		go func() {
			<-start
			acquired <- backend.TryAcquire()
		}()
		go func() {
			<-start
			pool.DrainBackend("api")
			close(drained)
		}()
		close(start)
		wasAcquired := <-acquired
		<-drained
		assert.False(t, backend.TryAcquire())
		if wasAcquired {
			backend.Release()
		}
		require.Equal(t, int64(0), backend.Inflight())
	}
}

func TestBackendSlowStartProgressesToFullTraffic(t *testing.T) {
	pool, err := balancer.NewBackendPool([]balancer.BackendSpec{{ID: "api", URL: "http://api:80"}}, balancer.PassivePolicy{FailureThreshold: 2, Cooldown: time.Second, MaxConcurrentRequests: 8, SlowStart: 30 * time.Millisecond, SlowStartMinimum: 20})
	require.NoError(t, err)
	backend := pool.GetBackends()[0]
	backend.SetAlive(true)
	assert.GreaterOrEqual(t, backend.SlowStartPercent(), 20)
	assert.Less(t, backend.SlowStartPercent(), 100)
	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, 100, backend.SlowStartPercent())
}
