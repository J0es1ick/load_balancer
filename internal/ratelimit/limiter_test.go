package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimiterFailurePolicies(t *testing.T) {
	settings := ratelimit.RuntimeSettings{Enabled: true, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 1}, FailureMode: "fail-closed", OperationTimeout: time.Second}
	limiter, err := ratelimit.NewTokenBucketLimiter(settings, failingStore{}, ratelimit.NewLocalStore(4))
	require.NoError(t, err)
	_, err = limiter.Allow(context.Background(), "client")
	assert.Error(t, err)

	settings.FailureMode = "fail-open"
	require.NoError(t, limiter.Reconfigure(settings))
	decision, err := limiter.AllowWithState(context.Background(), "client")
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.True(t, decision.Bucket.Degraded)

	settings.FailureMode = "local-fallback"
	require.NoError(t, limiter.Reconfigure(settings))
	first, err := limiter.AllowWithState(context.Background(), "client")
	require.NoError(t, err)
	second, err := limiter.AllowWithState(context.Background(), "client")
	require.NoError(t, err)
	assert.True(t, first.Allowed)
	assert.False(t, second.Allowed)
	assert.True(t, second.Bucket.Degraded)
}

func TestLimiterReconfigureIsInMemoryAndImmediate(t *testing.T) {
	store := ratelimit.NewLocalStore(4)
	settings := ratelimit.RuntimeSettings{Enabled: true, Policy: ratelimit.Policy{Capacity: 10, RefillPerSecond: 1}, FailureMode: "fail-open", OperationTimeout: time.Second}
	limiter, err := ratelimit.NewTokenBucketLimiter(settings, store, nil)
	require.NoError(t, err)
	settings.Policy.Capacity = 4
	settings.Policy.RefillPerSecond = 2.5
	require.NoError(t, limiter.Reconfigure(settings))
	state, err := limiter.Snapshot(context.Background(), "client")
	require.NoError(t, err)
	assert.Equal(t, 4, state.Capacity)
	assert.Equal(t, 2.5, state.RefillPerSecond)
}

func TestLimiterAggregatesIPv6ClientsByPrefix(t *testing.T) {
	settings := ratelimit.RuntimeSettings{Enabled: true, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 0.001}, FailureMode: "fail-closed", OperationTimeout: time.Second, IPv4PrefixBits: 32, IPv6PrefixBits: 64}
	limiter, err := ratelimit.NewTokenBucketLimiter(settings, ratelimit.NewBoundedLocalStore(1, 10), nil)
	require.NoError(t, err)

	first, err := limiter.Allow(context.Background(), "2001:db8:1::1")
	require.NoError(t, err)
	samePrefix, err := limiter.Allow(context.Background(), "2001:db8:1::ffff")
	require.NoError(t, err)
	otherPrefix, err := limiter.Allow(context.Background(), "2001:db8:2::1")
	require.NoError(t, err)

	assert.True(t, first)
	assert.False(t, samePrefix, "rotating an IPv6 interface identifier must not create a new bucket")
	assert.True(t, otherPrefix)
}

type failingStore struct{}

func (failingStore) Name() string { return "failing" }
func (failingStore) Take(context.Context, string, ratelimit.Policy) (ratelimit.LimitDecision, error) {
	return ratelimit.LimitDecision{}, errors.New("unavailable")
}
func (failingStore) Peek(context.Context, string, ratelimit.Policy) (ratelimit.BucketState, error) {
	return ratelimit.BucketState{}, errors.New("unavailable")
}
func (failingStore) Reset(context.Context, string, ratelimit.Policy) (ratelimit.BucketState, error) {
	return ratelimit.BucketState{}, errors.New("unavailable")
}
func (failingStore) Healthy(context.Context) error                { return errors.New("unavailable") }
func (failingStore) Cleanup(context.Context, time.Duration) error { return nil }
func (failingStore) Close() error                                 { return nil }
