package ratelimit_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisStoreIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("TEST_REDIS_ADDRESS is not set")
	}
	store, err := ratelimit.NewRedisStore(context.Background(), ratelimit.RedisOptions{Address: address, PoolSize: 32, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, Retention: time.Minute, Prefix: "integration:" + time.Now().Format("150405.000000") + ":"})
	require.NoError(t, err)
	defer store.Close()
	assertConcurrentLimit(t, store)
}

func TestRedisStoreConnectsLazilySoFailurePolicyCanHandleColdStart(t *testing.T) {
	store, err := ratelimit.NewRedisStore(context.Background(), ratelimit.RedisOptions{Address: "127.0.0.1:1", PoolSize: 1, DialTimeout: 10 * time.Millisecond, ReadTimeout: 10 * time.Millisecond, WriteTimeout: 10 * time.Millisecond, Retention: time.Minute})
	require.NoError(t, err)
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.Error(t, store.Healthy(ctx))
}

func TestRedisLimiterAppliesFailurePoliciesAfterConnectionLoss(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("TEST_REDIS_ADDRESS is not set")
	}
	store, err := ratelimit.NewRedisStore(context.Background(), ratelimit.RedisOptions{Address: address, PoolSize: 4, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, Retention: time.Minute, Prefix: "failure-policy:" + time.Now().Format("150405.000000") + ":"})
	require.NoError(t, err)
	require.NoError(t, store.Healthy(context.Background()))
	require.NoError(t, store.Close())
	assertLimiterFailurePoliciesAfterConnectionLoss(t, store)
}

func TestPostgresStoreIntegration(t *testing.T) {
	host := os.Getenv("TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("TEST_POSTGRES_HOST is not set")
	}
	store, err := ratelimit.NewPostgresStore(context.Background(), ratelimit.PostgresOptions{Host: host, Port: envOr("TEST_POSTGRES_PORT", "5432"), User: envOr("TEST_POSTGRES_USER", "postgres"), Password: os.Getenv("TEST_POSTGRES_PASSWORD"), Database: envOr("TEST_POSTGRES_DATABASE", "balancer"), SSLMode: "disable", ConnectTimeout: 5 * time.Second, MaxOpenConns: 25})
	require.NoError(t, err)
	defer store.Close()
	assertConcurrentLimit(t, store)
}

func TestPostgresMigrationsAreSerializedAcrossReplicas(t *testing.T) {
	host := os.Getenv("TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("TEST_POSTGRES_HOST is not set")
	}
	options := ratelimit.PostgresOptions{Host: host, Port: envOr("TEST_POSTGRES_PORT", "5432"), User: envOr("TEST_POSTGRES_USER", "postgres"), Password: os.Getenv("TEST_POSTGRES_PASSWORD"), Database: envOr("TEST_POSTGRES_DATABASE", "balancer"), SSLMode: "disable", ConnectTimeout: 5 * time.Second, MaxOpenConns: 5}
	errors := make(chan error, 6)
	var waitGroup sync.WaitGroup
	for range 6 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			store, err := ratelimit.NewPostgresStore(context.Background(), options)
			if err == nil {
				err = store.Close()
			}
			errors <- err
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func TestPostgresLimiterAppliesFailurePoliciesAfterConnectionLoss(t *testing.T) {
	host := os.Getenv("TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("TEST_POSTGRES_HOST is not set")
	}
	store, err := ratelimit.NewPostgresStore(context.Background(), ratelimit.PostgresOptions{Host: host, Port: envOr("TEST_POSTGRES_PORT", "5432"), User: envOr("TEST_POSTGRES_USER", "postgres"), Password: os.Getenv("TEST_POSTGRES_PASSWORD"), Database: envOr("TEST_POSTGRES_DATABASE", "balancer"), SSLMode: "disable", ConnectTimeout: 5 * time.Second, MaxOpenConns: 4})
	require.NoError(t, err)
	require.NoError(t, store.Healthy(context.Background()))
	require.NoError(t, store.Close())
	assertLimiterFailurePoliciesAfterConnectionLoss(t, store)
}

func assertLimiterFailurePoliciesAfterConnectionLoss(t *testing.T, store ratelimit.Store) {
	t.Helper()
	settings := ratelimit.RuntimeSettings{Enabled: true, Policy: ratelimit.Policy{Capacity: 1, RefillPerSecond: 0.001}, FailureMode: "fail-closed", OperationTimeout: 100 * time.Millisecond}
	limiter, err := ratelimit.NewTokenBucketLimiter(settings, store, ratelimit.NewLocalStore(4))
	require.NoError(t, err)

	_, err = limiter.AllowWithState(context.Background(), "connection-loss-client")
	assert.Error(t, err, "fail-closed must reject when the configured store is unavailable")

	settings.FailureMode = "fail-open"
	require.NoError(t, limiter.Reconfigure(settings))
	openDecision, err := limiter.AllowWithState(context.Background(), "connection-loss-client")
	require.NoError(t, err)
	assert.True(t, openDecision.Allowed)
	assert.True(t, openDecision.Bucket.Degraded)

	settings.FailureMode = "local-fallback"
	require.NoError(t, limiter.Reconfigure(settings))
	first, err := limiter.AllowWithState(context.Background(), "connection-loss-client")
	require.NoError(t, err)
	second, err := limiter.AllowWithState(context.Background(), "connection-loss-client")
	require.NoError(t, err)
	assert.True(t, first.Allowed)
	assert.False(t, second.Allowed)
	assert.True(t, first.Bucket.Degraded)
	assert.True(t, second.Bucket.Degraded)
}

func assertConcurrentLimit(t *testing.T, store ratelimit.Store) {
	t.Helper()
	key := "client-" + time.Now().Format("150405.000000000")
	policy := ratelimit.Policy{Capacity: 20, RefillPerSecond: 0.0001}
	var allowed atomic.Int64
	var waitGroup sync.WaitGroup
	errors := make(chan error, 100)
	for range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			decision, err := store.Take(context.Background(), key, policy)
			if err != nil {
				errors <- err
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	assert.Equal(t, int64(20), allowed.Load())
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
