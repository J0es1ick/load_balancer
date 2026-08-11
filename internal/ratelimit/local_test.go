package ratelimit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalStoreTokenBucketAndLazyReconfigure(t *testing.T) {
	store := ratelimit.NewLocalStore(16)
	policy := ratelimit.Policy{Capacity: 2, RefillPerSecond: 100}
	first, err := store.Take(context.Background(), "client", policy)
	require.NoError(t, err)
	second, _ := store.Take(context.Background(), "client", policy)
	third, _ := store.Take(context.Background(), "client", policy)
	assert.True(t, first.Allowed)
	assert.True(t, second.Allowed)
	assert.False(t, third.Allowed)
	time.Sleep(15 * time.Millisecond)
	refilled, _ := store.Take(context.Background(), "client", policy)
	assert.True(t, refilled.Allowed)

	reconfigured, err := store.Peek(context.Background(), "client", ratelimit.Policy{Capacity: 1, RefillPerSecond: 5})
	require.NoError(t, err)
	assert.Equal(t, 1, reconfigured.Capacity)
	assert.LessOrEqual(t, reconfigured.Tokens, 1.0)
}

func TestLocalStoreConcurrentCapacity(t *testing.T) {
	store := ratelimit.NewLocalStore(32)
	policy := ratelimit.Policy{Capacity: 20, RefillPerSecond: 0.001}
	var allowed int
	var mutex sync.Mutex
	var waitGroup sync.WaitGroup
	for range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			decision, err := store.Take(context.Background(), "same-client", policy)
			require.NoError(t, err)
			if decision.Allowed {
				mutex.Lock()
				allowed++
				mutex.Unlock()
			}
		}()
	}
	waitGroup.Wait()
	assert.Equal(t, 20, allowed)
}

func TestLocalStoreEvictsInConstantTimeAtConfiguredLimit(t *testing.T) {
	store := ratelimit.NewBoundedLocalStore(1, 3)
	policy := ratelimit.Policy{Capacity: 1, RefillPerSecond: 0.001}
	for _, key := range []string{"one", "two", "three", "four", "five"} {
		_, err := store.Take(context.Background(), key, policy)
		require.NoError(t, err)
	}
	stats := store.Stats()
	assert.Equal(t, int64(3), stats.Buckets)
	assert.Equal(t, uint64(2), stats.Evictions)
}

func TestLocalStoreReusesSlotsAfterCleanup(t *testing.T) {
	store := ratelimit.NewBoundedLocalStore(1, 3)
	policy := ratelimit.Policy{Capacity: 1, RefillPerSecond: 0.001}
	for _, key := range []string{"one", "two", "three"} {
		_, err := store.Take(context.Background(), key, policy)
		require.NoError(t, err)
	}
	require.NoError(t, store.Cleanup(context.Background(), -time.Second))
	assert.Zero(t, store.Stats().Buckets)
	for _, key := range []string{"four", "five", "six", "seven"} {
		_, err := store.Take(context.Background(), key, policy)
		require.NoError(t, err)
	}
	assert.Equal(t, int64(3), store.Stats().Buckets)
	assert.Equal(t, uint64(1), store.Stats().Evictions)
}

func BenchmarkLocalStoreTake(b *testing.B) {
	store := ratelimit.NewLocalStore(64)
	policy := ratelimit.Policy{Capacity: 1_000_000_000, RefillPerSecond: 1_000_000}
	b.ReportAllocs()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			_, _ = store.Take(context.Background(), "client", policy)
		}
	})
}
