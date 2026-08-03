package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/J0es1ick/test-assignment/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenBucketLimiter(t *testing.T) {
	t.Run("should allow when tokens available", func(t *testing.T) {
		mockStorage := &mockStorage{
			bucket: ratelimit.NewTokenBucket(10, time.Second),
		}

		limiter := ratelimit.NewTokenBucketLimiter(10, time.Second, mockStorage)
		allowed, err := limiter.Allow(context.Background(), "test")

		assert.True(t, allowed)
		assert.NoError(t, err)
	})

	t.Run("should reject when rate limit exceeded", func(t *testing.T) {
		mockStorage := &mockStorage{
			bucket: ratelimit.NewTokenBucket(1, time.Minute),
		}

		limiter := ratelimit.NewTokenBucketLimiter(1, time.Minute, mockStorage)

		allowed, err := limiter.Allow(context.Background(), "test")
		assert.True(t, allowed)
		assert.NoError(t, err)

		allowed, err = limiter.Allow(context.Background(), "test")
		assert.False(t, allowed)
		assert.NoError(t, err)
	})

	t.Run("should reconfigure existing buckets", func(t *testing.T) {
		storage := &mockStorage{
			bucket: ratelimit.NewTokenBucket(10, time.Second),
		}
		limiter := ratelimit.NewTokenBucketLimiter(10, time.Second, storage)

		require.NoError(t, limiter.Reconfigure(context.Background(), 4, 2*time.Second))
		state, err := limiter.Snapshot(context.Background(), "test")
		require.NoError(t, err)
		assert.Equal(t, 4, state.Capacity)
		assert.Equal(t, "2s", state.Rate)
	})

	t.Run("should process different keys concurrently", func(t *testing.T) {
		storage := &concurrentStorage{
			entered: make(chan string, 2),
			release: make(chan struct{}),
		}
		limiter := ratelimit.NewTokenBucketLimiter(10, time.Second, storage)
		results := make(chan error, 2)

		go func() {
			_, err := limiter.Allow(context.Background(), "first")
			results <- err
		}()
		go func() {
			_, err := limiter.Allow(context.Background(), "second")
			results <- err
		}()

		for range 2 {
			select {
			case <-storage.entered:
			case <-time.After(time.Second):
				t.Fatal("requests for different keys were serialized")
			}
		}
		close(storage.release)
		require.NoError(t, <-results)
		require.NoError(t, <-results)
	})
}

type mockStorage struct {
	bucket *ratelimit.TokenBucket
}

func (m *mockStorage) Get(ctx context.Context, key string) (*ratelimit.TokenBucket, bool, error) {
	if m.bucket == nil {
		return nil, false, nil
	}
	return m.bucket, true, nil
}

func (m *mockStorage) Set(ctx context.Context, key string, bucket *ratelimit.TokenBucket) error {
	m.bucket = bucket
	return nil
}

func (m *mockStorage) Update(ctx context.Context, key string, updateFunc func(*ratelimit.TokenBucket) (*ratelimit.TokenBucket, error)) error {
	newBucket, err := updateFunc(m.bucket)
	if err != nil {
		return err
	}
	m.bucket = newBucket
	return nil
}

func (m *mockStorage) Reconfigure(_ context.Context, capacity int, rate time.Duration) error {
	m.bucket = ratelimit.NewTokenBucket(capacity, rate)
	return nil
}

type concurrentStorage struct {
	entered chan string
	release chan struct{}
}

func (s *concurrentStorage) Get(context.Context, string) (*ratelimit.TokenBucket, bool, error) {
	return nil, false, nil
}

func (s *concurrentStorage) Set(context.Context, string, *ratelimit.TokenBucket) error {
	return nil
}

func (s *concurrentStorage) Update(
	_ context.Context,
	key string,
	update func(*ratelimit.TokenBucket) (*ratelimit.TokenBucket, error),
) error {
	s.entered <- key
	<-s.release
	_, err := update(nil)
	return err
}
